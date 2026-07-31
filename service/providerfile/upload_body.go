package providerfile

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"strings"

	"github.com/QuantumNous/new-api/common"
	openai "github.com/QuantumNous/new-api/relay/channel/openai"
)

const maximumManagedProviderFileMultipartOverhead = 1024 * 1024

var ErrInvalidUploadBody = errors.New("managed provider file upload body is invalid")

type UploadBody struct {
	storage       common.BodyStorage
	boundary      string
	Filename      string
	SizeBytes     int64
	ContentDigest string
}

func ParseUploadBody(storage common.BodyStorage, contentType string) (*UploadBody, error) {
	if storage == nil || storage.Size() <= 0 || storage.Size() > openai.OpenAIProviderFileMaximumUploadBytes+maximumManagedProviderFileMultipartOverhead {
		return nil, ErrInvalidUploadBody
	}
	mediaType, parameters, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" || strings.TrimSpace(parameters["boundary"]) == "" {
		return nil, ErrInvalidUploadBody
	}
	if _, err := storage.Seek(0, io.SeekStart); err != nil {
		return nil, ErrInvalidUploadBody
	}
	reader := multipart.NewReader(storage, parameters["boundary"])
	fileCount := 0
	purposeCount := 0
	partCount := 0
	var filename string
	var sizeBytes int64
	var contentDigest string
	for {
		part, partErr := reader.NextPart()
		if errors.Is(partErr, io.EOF) {
			break
		}
		if partErr != nil {
			return nil, ErrInvalidUploadBody
		}
		partCount++
		if partCount > 4 {
			return nil, ErrInvalidUploadBody
		}
		switch part.FormName() {
		case "purpose":
			if part.FileName() != "" || purposeCount != 0 {
				return nil, ErrInvalidUploadBody
			}
			value, readErr := io.ReadAll(io.LimitReader(part, 65))
			if readErr != nil || len(value) > 64 || string(value) != openai.OpenAIProviderFilePurposeUserData {
				return nil, ErrInvalidUploadBody
			}
			purposeCount++
		case "file":
			if fileCount != 0 || part.FileName() == "" {
				return nil, ErrInvalidUploadBody
			}
			digest := sha256.New()
			written, readErr := io.Copy(digest, io.LimitReader(part, openai.OpenAIProviderFileMaximumUploadBytes+1))
			if readErr != nil || written <= 0 || written > openai.OpenAIProviderFileMaximumUploadBytes {
				return nil, ErrInvalidUploadBody
			}
			filename = part.FileName()
			sizeBytes = written
			contentDigest = hex.EncodeToString(digest.Sum(nil))
			fileCount++
		default:
			return nil, ErrInvalidUploadBody
		}
	}
	if fileCount != 1 || purposeCount != 1 {
		return nil, ErrInvalidUploadBody
	}
	return &UploadBody{
		storage: storage, boundary: parameters["boundary"], Filename: filename, SizeBytes: sizeBytes, ContentDigest: contentDigest,
	}, nil
}

func (body *UploadBody) Open() (io.ReadCloser, error) {
	if body == nil || body.storage == nil || strings.TrimSpace(body.boundary) == "" {
		return nil, ErrInvalidUploadBody
	}
	if _, err := body.storage.Seek(0, io.SeekStart); err != nil {
		return nil, ErrInvalidUploadBody
	}
	reader := multipart.NewReader(body.storage, body.boundary)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, ErrInvalidUploadBody
		}
		if err != nil {
			return nil, fmt.Errorf("%w", ErrInvalidUploadBody)
		}
		if part.FormName() == "file" && part.FileName() == body.Filename {
			return part, nil
		}
	}
}
