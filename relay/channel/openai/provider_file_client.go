package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
)

const (
	OpenAIProviderFileOrigin               = "https://api.openai.com"
	OpenAIProviderFilePurposeUserData      = "user_data"
	OpenAIProviderFileMaximumUploadBytes   = 50 * 1024 * 1024
	OpenAIProviderFileMinimumExpirySeconds = 60
	OpenAIProviderFileMaximumExpirySeconds = 30 * 24 * 60 * 60
	openAIProviderFileMaximumResponseBytes = 64 * 1024
)

var (
	ErrOpenAIProviderFileClientConfiguration = errors.New("OpenAI provider file client configuration is invalid")
	ErrOpenAIProviderFileRequest             = errors.New("OpenAI provider file request is invalid")
	ErrOpenAIProviderFileTransport           = errors.New("OpenAI provider file transport failed")
	ErrOpenAIProviderFileUpstreamStatus      = errors.New("OpenAI provider file upstream status is unsuccessful")
	ErrOpenAIProviderFileResponseTooLarge    = errors.New("OpenAI provider file response exceeds the limit")
	ErrOpenAIProviderFileResponse            = errors.New("OpenAI provider file response is invalid")
)

type ProviderFileClient struct {
	httpClient   *http.Client
	endpoint     *url.URL
	apiKey       string
	organization string
	now          func() time.Time
}

type ProviderFileUploadRequest struct {
	Filename            string
	Content             io.Reader
	SizeBytes           int64
	ExpiresAfterSeconds int
}

type ProviderFileMetadata struct {
	ProviderFileID string `json:"-"`
	Filename       string `json:"-"`
	Object         string `json:"object"`
	Purpose        string `json:"purpose"`
	Bytes          int64  `json:"bytes"`
	CreatedAtUnix  int64  `json:"created_at"`
	ExpiresAtUnix  int64  `json:"expires_at"`
}

type providerFileWireMetadata struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Bytes     int64  `json:"bytes"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	Filename  string `json:"filename"`
	Purpose   string `json:"purpose"`
}

type ProviderFileUpstreamStatusError struct {
	StatusCode int
}

func (err ProviderFileUpstreamStatusError) Error() string {
	return fmt.Sprintf("OpenAI provider file upstream returned HTTP status %d", err.StatusCode)
}

func (err ProviderFileUpstreamStatusError) Unwrap() error {
	return ErrOpenAIProviderFileUpstreamStatus
}

func (metadata ProviderFileMetadata) String() string {
	return "ProviderFileMetadata{ProviderFileID:***masked***,Filename:***masked***}"
}

func (metadata ProviderFileMetadata) GoString() string {
	return metadata.String()
}

func NewProviderFileClient(httpClient *http.Client, endpoint, apiKey, organization string) (*ProviderFileClient, error) {
	if httpClient == nil || !validProviderFileHeaderValue(apiKey, false) || !validProviderFileHeaderValue(organization, true) {
		return nil, ErrOpenAIProviderFileClientConfiguration
	}
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil || parsedEndpoint.Scheme != "https" || parsedEndpoint.Host != "api.openai.com" || parsedEndpoint.Hostname() != "api.openai.com" ||
		parsedEndpoint.Port() != "" || parsedEndpoint.User != nil || (parsedEndpoint.Path != "" && parsedEndpoint.Path != "/") ||
		parsedEndpoint.RawQuery != "" || parsedEndpoint.Fragment != "" {
		return nil, ErrOpenAIProviderFileClientConfiguration
	}
	isolatedHTTPClient := *httpClient
	isolatedHTTPClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &ProviderFileClient{
		httpClient: &isolatedHTTPClient, endpoint: parsedEndpoint, apiKey: apiKey, organization: organization, now: time.Now,
	}, nil
}

func (client *ProviderFileClient) Upload(ctx context.Context, upload ProviderFileUploadRequest) (ProviderFileMetadata, error) {
	if client == nil || ctx == nil || upload.Content == nil || !validProviderFileFilename(upload.Filename) ||
		upload.SizeBytes <= 0 || upload.SizeBytes > OpenAIProviderFileMaximumUploadBytes ||
		upload.ExpiresAfterSeconds < OpenAIProviderFileMinimumExpirySeconds || upload.ExpiresAfterSeconds > OpenAIProviderFileMaximumExpirySeconds {
		return ProviderFileMetadata{}, ErrOpenAIProviderFileRequest
	}
	requestBody, requestBodyWriter := io.Pipe()
	multipartWriter := multipart.NewWriter(requestBodyWriter)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.requestURL("/v1/files"), requestBody)
	if err != nil {
		requestBody.Close()
		requestBodyWriter.Close()
		return ProviderFileMetadata{}, ErrOpenAIProviderFileRequest
	}
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	client.setHeaders(request)

	writeResult := make(chan error, 1)
	go func() {
		writeError := writeProviderFileMultipart(multipartWriter, upload)
		if writeError != nil {
			requestBodyWriter.CloseWithError(writeError)
		} else {
			requestBodyWriter.Close()
		}
		writeResult <- writeError
	}()

	response, transportErr := client.httpClient.Do(request)
	requestBody.Close()
	writeErr := <-writeResult
	if writeErr != nil {
		if response != nil {
			response.Body.Close()
		}
		return ProviderFileMetadata{}, ErrOpenAIProviderFileRequest
	}
	if transportErr != nil {
		return ProviderFileMetadata{}, ErrOpenAIProviderFileTransport
	}
	defer response.Body.Close()
	metadata, err := client.decodeMetadataResponse(response, "", upload.Filename, upload.SizeBytes)
	if err != nil {
		return ProviderFileMetadata{}, err
	}
	return metadata, nil
}

func (client *ProviderFileClient) Retrieve(ctx context.Context, providerFileID string) (ProviderFileMetadata, error) {
	if client == nil || ctx == nil || !validProviderFileID(providerFileID) {
		return ProviderFileMetadata{}, ErrOpenAIProviderFileRequest
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.requestURL("/v1/files/"+url.PathEscape(providerFileID)), nil)
	if err != nil {
		return ProviderFileMetadata{}, ErrOpenAIProviderFileRequest
	}
	client.setHeaders(request)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return ProviderFileMetadata{}, ErrOpenAIProviderFileTransport
	}
	defer response.Body.Close()
	return client.decodeMetadataResponse(response, providerFileID, "", 0)
}

func writeProviderFileMultipart(writer *multipart.Writer, upload ProviderFileUploadRequest) error {
	if err := writer.WriteField("purpose", OpenAIProviderFilePurposeUserData); err != nil {
		return err
	}
	if err := writer.WriteField("expires_after[anchor]", "created_at"); err != nil {
		return err
	}
	if err := writer.WriteField("expires_after[seconds]", fmt.Sprintf("%d", upload.ExpiresAfterSeconds)); err != nil {
		return err
	}
	filePart, err := writer.CreateFormFile("file", upload.Filename)
	if err != nil {
		return err
	}
	written, err := io.CopyN(filePart, upload.Content, upload.SizeBytes)
	if err != nil || written != upload.SizeBytes {
		return errors.New("provider file content length is invalid")
	}
	return writer.Close()
}

func (client *ProviderFileClient) decodeMetadataResponse(response *http.Response, expectedProviderFileID, expectedFilename string, expectedBytes int64) (ProviderFileMetadata, error) {
	if response.StatusCode != http.StatusOK {
		return ProviderFileMetadata{}, ProviderFileUpstreamStatusError{StatusCode: response.StatusCode}
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return ProviderFileMetadata{}, ErrOpenAIProviderFileResponse
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, openAIProviderFileMaximumResponseBytes+1))
	if err != nil {
		return ProviderFileMetadata{}, ErrOpenAIProviderFileResponse
	}
	if len(responseBody) > openAIProviderFileMaximumResponseBytes {
		return ProviderFileMetadata{}, ErrOpenAIProviderFileResponseTooLarge
	}
	var wireMetadata providerFileWireMetadata
	if err := common.Unmarshal(responseBody, &wireMetadata); err != nil {
		return ProviderFileMetadata{}, ErrOpenAIProviderFileResponse
	}
	nowUnix := client.now().UTC().Unix()
	if wireMetadata.Object != "file" || wireMetadata.Purpose != OpenAIProviderFilePurposeUserData || !validProviderFileID(wireMetadata.ID) ||
		!validProviderFileFilename(wireMetadata.Filename) || wireMetadata.Bytes <= 0 || wireMetadata.CreatedAt <= 0 ||
		wireMetadata.ExpiresAt <= wireMetadata.CreatedAt || wireMetadata.ExpiresAt <= nowUnix ||
		(expectedProviderFileID != "" && wireMetadata.ID != expectedProviderFileID) ||
		(expectedFilename != "" && wireMetadata.Filename != expectedFilename) || (expectedBytes > 0 && wireMetadata.Bytes != expectedBytes) {
		return ProviderFileMetadata{}, ErrOpenAIProviderFileResponse
	}
	return ProviderFileMetadata{
		ProviderFileID: wireMetadata.ID, Filename: wireMetadata.Filename, Object: wireMetadata.Object,
		Purpose: wireMetadata.Purpose, Bytes: wireMetadata.Bytes, CreatedAtUnix: wireMetadata.CreatedAt, ExpiresAtUnix: wireMetadata.ExpiresAt,
	}, nil
}

func (client *ProviderFileClient) requestURL(path string) string {
	requestURL := *client.endpoint
	requestURL.Path = path
	return requestURL.String()
}

func (client *ProviderFileClient) setHeaders(request *http.Request) {
	request.Header.Set("Authorization", "Bearer "+client.apiKey)
	if client.organization != "" {
		request.Header.Set("OpenAI-Organization", client.organization)
	}
}

func validProviderFileHeaderValue(value string, optional bool) bool {
	if value == "" {
		return optional
	}
	if len(value) > 4096 || strings.TrimSpace(value) != value {
		return false
	}
	for index := range len(value) {
		if value[index] < 0x21 || value[index] > 0x7e {
			return false
		}
	}
	return true
}

func validProviderFileFilename(filename string) bool {
	if filename == "" || len(filename) > 255 || !utf8.ValidString(filename) || strings.TrimSpace(filename) != filename ||
		strings.ContainsAny(filename, "\x00\r\n/\\") {
		return false
	}
	return true
}

func validProviderFileID(providerFileID string) bool {
	if !strings.HasPrefix(providerFileID, "file-") || len(providerFileID) <= len("file-") || len(providerFileID) > 512 {
		return false
	}
	return strings.IndexFunc(strings.TrimPrefix(providerFileID, "file-"), func(character rune) bool {
		return !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_')
	}) == -1
}
