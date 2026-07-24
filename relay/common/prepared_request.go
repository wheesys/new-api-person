package common

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sync"

	appcommon "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
)

type PreparedRelayRequestMetadata struct {
	Model              string
	Protocol           types.RelayFormat
	RequestedMaxOutput *uint
}

// PreparedRelayRequest is the immutable final upstream request snapshot.
// Reader always rewinds the same storage that was used to calculate metadata.
type PreparedRelayRequest struct {
	model              string
	protocol           types.RelayFormat
	bodyDigest         string
	size               int64
	requestedMaxOutput *uint
	storage            appcommon.BodyStorage
	ownsStorage        bool
	closeOnce          sync.Once
	closeErr           error
}

func PrepareJSONRelayRequest(body []byte, metadata PreparedRelayRequestMetadata) (*PreparedRelayRequest, error) {
	bodySnapshot := append([]byte(nil), body...)
	storage, err := appcommon.CreateBodyStorage(bodySnapshot)
	if err != nil {
		return nil, fmt.Errorf("create prepared request body storage: %w", err)
	}
	prepared, err := newPreparedRelayRequest(storage, metadata, true)
	if err != nil {
		_ = storage.Close()
		return nil, err
	}
	return prepared, nil
}

func PreparePassThroughRelayRequest(storage appcommon.BodyStorage, metadata PreparedRelayRequestMetadata) (*PreparedRelayRequest, error) {
	if storage == nil {
		return nil, fmt.Errorf("pass-through body storage is required")
	}
	return newPreparedRelayRequest(storage, metadata, false)
}

func PrepareFinalJSONRelayRequest(info *RelayInfo, body []byte) (*PreparedRelayRequest, error) {
	if info == nil {
		return nil, fmt.Errorf("relay info is required")
	}
	prepared, err := PrepareJSONRelayRequest(body, PreparedRelayRequestMetadata{
		Model:    info.UpstreamModelName,
		Protocol: info.GetFinalRequestRelayFormat(),
	})
	if err != nil {
		return nil, err
	}
	info.RecordPreparedRelayRequest(prepared)
	return prepared, nil
}

func PrepareFinalPassThroughRelayRequest(info *RelayInfo, storage appcommon.BodyStorage) (*PreparedRelayRequest, error) {
	if info == nil {
		return nil, fmt.Errorf("relay info is required")
	}
	prepared, err := PreparePassThroughRelayRequest(storage, PreparedRelayRequestMetadata{
		Model:    info.UpstreamModelName,
		Protocol: info.GetFinalRequestRelayFormat(),
	})
	if err != nil {
		return nil, err
	}
	info.RecordPreparedRelayRequest(prepared)
	return prepared, nil
}

func newPreparedRelayRequest(storage appcommon.BodyStorage, metadata PreparedRelayRequestMetadata, ownsStorage bool) (*PreparedRelayRequest, error) {
	bodyDigest, err := digestBodyStorage(storage)
	if err != nil {
		return nil, fmt.Errorf("digest prepared request body: %w", err)
	}
	model, requestedMaxOutput, metadataReliable := extractPreparedRequestMetadata(storage, metadata.Protocol)
	if metadataReliable {
		if model != "" {
			metadata.Model = model
		}
		metadata.RequestedMaxOutput = requestedMaxOutput
	}
	return &PreparedRelayRequest{
		model:              metadata.Model,
		protocol:           metadata.Protocol,
		bodyDigest:         bodyDigest,
		size:               storage.Size(),
		requestedMaxOutput: copyOptionalUint(metadata.RequestedMaxOutput),
		storage:            storage,
		ownsStorage:        ownsStorage,
	}, nil
}

func (request *PreparedRelayRequest) Model() string {
	if request == nil {
		return ""
	}
	return request.model
}

func (request *PreparedRelayRequest) Protocol() types.RelayFormat {
	if request == nil {
		return ""
	}
	return request.protocol
}

func (request *PreparedRelayRequest) BodyDigest() string {
	if request == nil {
		return ""
	}
	return request.bodyDigest
}

func (request *PreparedRelayRequest) Size() int64 {
	if request == nil {
		return 0
	}
	return request.size
}

func (request *PreparedRelayRequest) RequestedMaxOutput() *uint {
	if request == nil {
		return nil
	}
	return copyOptionalUint(request.requestedMaxOutput)
}

func (request *PreparedRelayRequest) Body() ([]byte, error) {
	if request == nil || request.storage == nil {
		return nil, fmt.Errorf("prepared request body storage is unavailable")
	}
	body, err := request.storage.Bytes()
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), body...), nil
}

func (request *PreparedRelayRequest) Reader() (io.Reader, error) {
	if request == nil || request.storage == nil {
		return nil, fmt.Errorf("prepared request body storage is unavailable")
	}
	if _, err := request.storage.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind prepared request body: %w", err)
	}
	return appcommon.ReaderOnly(request.storage), nil
}

func (request *PreparedRelayRequest) Close() error {
	if request == nil || !request.ownsStorage || request.storage == nil {
		return nil
	}
	request.closeOnce.Do(func() {
		request.closeErr = request.storage.Close()
	})
	return request.closeErr
}

func digestBodyStorage(storage appcommon.BodyStorage) (string, error) {
	if _, err := storage.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, storage); err != nil {
		return "", err
	}
	if _, err := storage.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func extractPreparedRequestMetadata(storage appcommon.BodyStorage, protocol types.RelayFormat) (string, *uint, bool) {
	if _, err := storage.Seek(0, io.SeekStart); err != nil {
		return "", nil, false
	}
	defer func() {
		_, _ = storage.Seek(0, io.SeekStart)
	}()

	switch protocol {
	case types.RelayFormatOpenAI:
		var request dto.GeneralOpenAIRequest
		if err := appcommon.DecodeJson(storage, &request); err != nil {
			return "", nil, false
		}
		requestedMaxOutput := request.MaxTokens
		if request.MaxCompletionTokens != nil {
			requestedMaxOutput = request.MaxCompletionTokens
		}
		return request.Model, requestedMaxOutput, true
	case types.RelayFormatOpenAIResponses:
		var request dto.OpenAIResponsesRequest
		if err := appcommon.DecodeJson(storage, &request); err != nil {
			return "", nil, false
		}
		return request.Model, request.MaxOutputTokens, true
	case types.RelayFormatOpenAIResponsesCompaction:
		var request dto.OpenAIResponsesCompactionRequest
		if err := appcommon.DecodeJson(storage, &request); err != nil {
			return "", nil, false
		}
		return request.Model, nil, true
	case types.RelayFormatClaude:
		var request dto.ClaudeRequest
		if err := appcommon.DecodeJson(storage, &request); err != nil {
			return "", nil, false
		}
		requestedMaxOutput := request.MaxTokens
		if request.MaxTokensToSample != nil {
			requestedMaxOutput = request.MaxTokensToSample
		}
		return request.Model, requestedMaxOutput, true
	case types.RelayFormatGemini:
		var request dto.GeminiChatRequest
		if err := appcommon.DecodeJson(storage, &request); err != nil {
			return "", nil, false
		}
		return "", request.GenerationConfig.MaxOutputTokens, true
	default:
		return "", nil, false
	}
}

func copyOptionalUint(value *uint) *uint {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}
