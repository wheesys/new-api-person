package providerfile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/contextconsensus"
)

const maximumManagedProviderFileReferences = 128

var ErrInvalidReference = errors.New("managed provider file reference is invalid")

type providerFileOccurrence struct {
	inputIndex   int
	contentIndex int
	handle       string
}

type parsedProviderFileRequest struct {
	root        map[string]json.RawMessage
	input       []json.RawMessage
	occurrences []providerFileOccurrence
}

type Resolution struct {
	channelID     int
	channelType   int
	credential    string
	endpoint      string
	organization  string
	expectedState contextconsensus.ProviderFileState
}

func HasManagedReferences(body []byte) (bool, error) {
	parsed, err := parseProviderFileRequest(body)
	if err != nil {
		return false, err
	}
	return len(parsed.occurrences) > 0, nil
}

func (resolution Resolution) String() string {
	return "Resolution{Credential:***masked***,References:***masked***}"
}

func (resolution Resolution) GoString() string {
	return resolution.String()
}

func (resolution *Resolution) ChannelID() int {
	if resolution == nil {
		return 0
	}
	return resolution.channelID
}

func (resolution *Resolution) ChannelType() int {
	if resolution == nil {
		return 0
	}
	return resolution.channelType
}

func (resolution *Resolution) Credential() string {
	if resolution == nil {
		return ""
	}
	return resolution.credential
}

func PrepareResolution(ctx context.Context, body []byte, owner contextconsensus.ManagedConsensusOwner, runtime *contextconsensus.ManagedConsensusRuntime, target *Target) (*Resolution, []byte, error) {
	parsed, err := parseProviderFileRequest(body)
	if err != nil {
		return nil, nil, err
	}
	if len(parsed.occurrences) == 0 {
		return nil, body, nil
	}
	providerIDs := make(map[string]string, len(parsed.occurrences))
	for _, occurrence := range parsed.occurrences {
		if _, exists := providerIDs[occurrence.handle]; exists {
			continue
		}
		_, payload, _, err := resolveActiveReference(ctx, RetrieveRequest{
			Owner: owner, Handle: occurrence.handle, Runtime: runtime, Target: target,
		})
		if err != nil {
			return nil, nil, err
		}
		providerIDs[occurrence.handle] = payload.ProviderFileID
	}
	rewrittenBody, err := parsed.rewrite(providerIDs)
	if err != nil {
		return nil, nil, ErrInvalidReference
	}
	expectedState, err := contextconsensus.ExtractProviderFileState(contextconsensus.ExtractionRequest{
		Protocol: types.RelayFormatOpenAIResponses, Body: rewrittenBody,
	})
	if err != nil || !expectedState.RequiresLifecycleValidation() || expectedState.Count(contextconsensus.ProviderFileReferenceProviderID) != len(parsed.occurrences) {
		return nil, nil, ErrInvalidReference
	}
	return &Resolution{
		channelID: target.ChannelID, channelType: target.ChannelType, credential: target.credential,
		endpoint: target.Endpoint, organization: target.Organization, expectedState: expectedState,
	}, rewrittenBody, nil
}

func (resolution *Resolution) ValidateFinalBody(body []byte) error {
	if resolution == nil {
		return ErrInvalidReference
	}
	actualState, err := contextconsensus.ExtractProviderFileState(contextconsensus.ExtractionRequest{
		Protocol: types.RelayFormatOpenAIResponses, Body: body,
	})
	if err != nil || len(actualState.References) != len(resolution.expectedState.References) ||
		len(actualState.ReasonCodes) != len(resolution.expectedState.ReasonCodes) {
		return ErrInvalidReference
	}
	for index := range actualState.References {
		actual := actualState.References[index]
		expected := resolution.expectedState.References[index]
		if actual.Protocol != expected.Protocol || actual.Sequence != expected.Sequence || actual.Kind != expected.Kind ||
			actual.RequiredBinding != expected.RequiredBinding || actual.ReferenceHMAC != expected.ReferenceHMAC {
			return ErrInvalidReference
		}
	}
	for index := range actualState.ReasonCodes {
		if actualState.ReasonCodes[index] != resolution.expectedState.ReasonCodes[index] {
			return ErrInvalidReference
		}
	}
	return nil
}

func parseProviderFileRequest(body []byte) (*parsedProviderFileRequest, error) {
	var root map[string]json.RawMessage
	if len(body) == 0 || common.Unmarshal(body, &root) != nil || root == nil {
		return nil, ErrInvalidReference
	}
	inputValue, exists := root["input"]
	if !exists {
		return &parsedProviderFileRequest{root: root}, nil
	}
	var input []json.RawMessage
	if err := common.Unmarshal(inputValue, &input); err != nil {
		var inputText string
		if common.Unmarshal(inputValue, &inputText) == nil {
			return &parsedProviderFileRequest{root: root}, nil
		}
		return nil, ErrInvalidReference
	}
	parsed := &parsedProviderFileRequest{root: root, input: input}
	for inputIndex, rawItem := range input {
		var item map[string]json.RawMessage
		if common.Unmarshal(rawItem, &item) != nil || item == nil {
			return nil, ErrInvalidReference
		}
		itemType, err := rawProviderFileString(item["type"])
		if err != nil {
			return nil, ErrInvalidReference
		}
		if itemType == "input_file" {
			handle, err := managedHandleFromPart(item)
			if err != nil {
				return nil, err
			}
			parsed.occurrences = append(parsed.occurrences, providerFileOccurrence{inputIndex: inputIndex, contentIndex: -1, handle: handle})
		}
		contentValue, hasContent := item["content"]
		if !hasContent {
			continue
		}
		var content []json.RawMessage
		if common.Unmarshal(contentValue, &content) != nil {
			continue
		}
		for contentIndex, rawPart := range content {
			var part map[string]json.RawMessage
			if common.Unmarshal(rawPart, &part) != nil || part == nil {
				return nil, ErrInvalidReference
			}
			partType, err := rawProviderFileString(part["type"])
			if err != nil {
				return nil, ErrInvalidReference
			}
			if partType != "input_file" {
				continue
			}
			handle, err := managedHandleFromPart(part)
			if err != nil {
				return nil, err
			}
			parsed.occurrences = append(parsed.occurrences, providerFileOccurrence{inputIndex: inputIndex, contentIndex: contentIndex, handle: handle})
		}
	}
	if len(parsed.occurrences) > maximumManagedProviderFileReferences {
		return nil, ErrInvalidReference
	}
	return parsed, nil
}

func managedHandleFromPart(part map[string]json.RawMessage) (string, error) {
	if _, exists := part["file_data"]; exists {
		return "", ErrInvalidReference
	}
	if _, exists := part["file_url"]; exists {
		return "", ErrInvalidReference
	}
	handle, err := rawProviderFileString(part["file_id"])
	if err != nil || contextconsensus.ValidateManagedProviderFileHandle(handle) != nil {
		return "", ErrInvalidReference
	}
	return handle, nil
}

func rawProviderFileString(value json.RawMessage) (string, error) {
	if len(value) == 0 {
		return "", nil
	}
	var decoded string
	if common.Unmarshal(value, &decoded) != nil || strings.TrimSpace(decoded) != decoded {
		return "", ErrInvalidReference
	}
	return decoded, nil
}

func (parsed *parsedProviderFileRequest) rewrite(providerIDs map[string]string) ([]byte, error) {
	if parsed == nil || parsed.root == nil {
		return nil, ErrInvalidReference
	}
	rewrittenInput := append([]json.RawMessage(nil), parsed.input...)
	for _, occurrence := range parsed.occurrences {
		providerID := providerIDs[occurrence.handle]
		if providerID == "" {
			return nil, ErrInvalidReference
		}
		var item map[string]json.RawMessage
		if common.Unmarshal(rewrittenInput[occurrence.inputIndex], &item) != nil {
			return nil, ErrInvalidReference
		}
		encodedProviderID, err := common.Marshal(providerID)
		if err != nil {
			return nil, err
		}
		if occurrence.contentIndex < 0 {
			item["file_id"] = encodedProviderID
		} else {
			var content []json.RawMessage
			if common.Unmarshal(item["content"], &content) != nil {
				return nil, ErrInvalidReference
			}
			var part map[string]json.RawMessage
			if common.Unmarshal(content[occurrence.contentIndex], &part) != nil {
				return nil, ErrInvalidReference
			}
			part["file_id"] = encodedProviderID
			content[occurrence.contentIndex], err = common.Marshal(part)
			if err != nil {
				return nil, err
			}
			item["content"], err = common.Marshal(content)
			if err != nil {
				return nil, err
			}
		}
		rewrittenInput[occurrence.inputIndex], err = common.Marshal(item)
		if err != nil {
			return nil, err
		}
	}
	encodedInput, err := common.Marshal(rewrittenInput)
	if err != nil {
		return nil, err
	}
	parsed.root["input"] = encodedInput
	return common.Marshal(parsed.root)
}

func (resolution *Resolution) ValidateTarget(channelID, channelType int, credential string) error {
	if resolution == nil || resolution.channelID != channelID || resolution.channelType != channelType || resolution.credential != credential {
		return fmt.Errorf("managed provider file target is unavailable")
	}
	return nil
}

func (resolution *Resolution) ValidateFinalTarget(channelID, channelType, multiKeyIndex int, channelIsMultiKey bool, endpoint, organization, credential string) error {
	if resolution == nil || resolution.channelID != channelID || resolution.channelType != channelType || multiKeyIndex != 0 || channelIsMultiKey ||
		resolution.endpoint != strings.TrimSuffix(endpoint, "/") || resolution.organization != organization || resolution.credential != credential {
		return fmt.Errorf("managed provider file final target is unavailable")
	}
	return nil
}
