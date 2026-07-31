package openai

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type providerFileRewriteTransport struct {
	target *url.URL
	base   http.RoundTripper
}

type providerFileRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip providerFileRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func (transport providerFileRewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	rewritten := request.Clone(request.Context())
	rewritten.URL.Scheme = transport.target.Scheme
	rewritten.URL.Host = transport.target.Host
	rewritten.Host = transport.target.Host
	return transport.base.RoundTrip(rewritten)
}

func newProviderFileTestClient(t *testing.T, handler http.Handler, apiKey, organization string, now time.Time) *ProviderFileClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	httpClient := server.Client()
	httpClient.Transport = providerFileRewriteTransport{target: target, base: httpClient.Transport}
	client, err := NewProviderFileClient(httpClient, OpenAIProviderFileOrigin, apiKey, organization)
	require.NoError(t, err)
	client.now = func() time.Time { return now }
	return client
}

func providerFileResponseBody(t *testing.T, values map[string]any) []byte {
	t.Helper()
	responseBody, err := common.Marshal(values)
	require.NoError(t, err)
	return responseBody
}

func TestProviderFileClientUploadStreamsMultipartAndValidatesMetadata(t *testing.T) {
	fixedNow := time.Unix(2_000_000_000, 0).UTC()
	apiKey := "sk-sensitive-provider-file-key"
	organization := "org-provider-file"
	filename := "contract.pdf"
	content := []byte("provider file content")
	responseBody := providerFileResponseBody(t, map[string]any{
		"id": "file-uploaded_1", "object": "file", "bytes": len(content), "created_at": fixedNow.Unix() - 10,
		"expires_at": fixedNow.Unix() + 3600, "filename": filename, "purpose": OpenAIProviderFilePurposeUserData,
	})
	handler := http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodPost, request.Method)
		assert.Equal(t, "/v1/files", request.URL.Path)
		assert.Equal(t, "Bearer "+apiKey, request.Header.Get("Authorization"))
		assert.Equal(t, organization, request.Header.Get("OpenAI-Organization"))
		assert.Equal(t, int64(-1), request.ContentLength, "multipart upload must remain streaming")

		multipartReader, err := request.MultipartReader()
		assert.NoError(t, err)
		fields := make(map[string]string)
		var uploadedContent []byte
		for {
			part, partErr := multipartReader.NextPart()
			if errors.Is(partErr, io.EOF) {
				break
			}
			if !assert.NoError(t, partErr) {
				return
			}
			partBody, readErr := io.ReadAll(part)
			if !assert.NoError(t, readErr) {
				return
			}
			if part.FormName() == "file" {
				assert.Equal(t, filename, part.FileName())
				uploadedContent = partBody
				continue
			}
			fields[part.FormName()] = string(partBody)
		}
		assert.Equal(t, OpenAIProviderFilePurposeUserData, fields["purpose"])
		assert.Equal(t, "created_at", fields["expires_after[anchor]"])
		assert.Equal(t, "3600", fields["expires_after[seconds]"])
		assert.NotContains(t, fields, "expires_after")
		assert.Equal(t, content, uploadedContent)
		responseWriter.Header().Set("Content-Type", "application/json; charset=utf-8")
		responseWriter.WriteHeader(http.StatusOK)
		_, _ = responseWriter.Write(responseBody)
	})
	client := newProviderFileTestClient(t, handler, apiKey, organization, fixedNow)

	metadata, err := client.Upload(context.Background(), ProviderFileUploadRequest{
		Filename: filename, Content: bytes.NewReader(content), SizeBytes: int64(len(content)), ExpiresAfterSeconds: 3600,
	})
	require.NoError(t, err)
	assert.Equal(t, "file-uploaded_1", metadata.ProviderFileID)
	assert.Equal(t, int64(len(content)), metadata.Bytes)
	assert.NotContains(t, fmt.Sprintf("%v", metadata), metadata.ProviderFileID)
	assert.NotContains(t, fmt.Sprintf("%#v", metadata), filename)
	serializedMetadata, err := common.Marshal(metadata)
	require.NoError(t, err)
	assert.NotContains(t, string(serializedMetadata), metadata.ProviderFileID)
	assert.NotContains(t, string(serializedMetadata), filename)
}

func TestProviderFileClientRetrieveUsesExactIdentity(t *testing.T) {
	fixedNow := time.Unix(2_000_000_000, 0).UTC()
	providerFileID := "file-retrieve_1"
	responseBody := providerFileResponseBody(t, map[string]any{
		"id": providerFileID, "object": "file", "bytes": 42, "created_at": fixedNow.Unix() - 20,
		"expires_at": fixedNow.Unix() + 3600, "filename": "facts.txt", "purpose": OpenAIProviderFilePurposeUserData,
	})
	handler := http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodGet, request.Method)
		assert.Equal(t, "/v1/files/"+providerFileID, request.URL.Path)
		assert.Equal(t, "Bearer sk-retrieve", request.Header.Get("Authorization"))
		assert.Empty(t, request.Header.Get("OpenAI-Organization"))
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write(responseBody)
	})
	client := newProviderFileTestClient(t, handler, "sk-retrieve", "", fixedNow)

	metadata, err := client.Retrieve(context.Background(), providerFileID)
	require.NoError(t, err)
	assert.Equal(t, providerFileID, metadata.ProviderFileID)
	assert.Equal(t, OpenAIProviderFilePurposeUserData, metadata.Purpose)
}

func TestProviderFileClientRejectsNonOfficialOriginAndUnsafeInputs(t *testing.T) {
	httpClient := &http.Client{}
	for _, endpoint := range []string{
		"http://api.openai.com", "https://api.openai.com.evil.example", "https://api.openai.com:443", "https://api.openai.com/v1", "https://user@api.openai.com",
	} {
		client, err := NewProviderFileClient(httpClient, endpoint, "sk-safe", "")
		assert.ErrorIs(t, err, ErrOpenAIProviderFileClientConfiguration)
		assert.Nil(t, client)
	}
	client, err := NewProviderFileClient(httpClient, OpenAIProviderFileOrigin, "sk-safe\r\nInjected: true", "")
	assert.ErrorIs(t, err, ErrOpenAIProviderFileClientConfiguration)
	assert.Nil(t, client)

	client = newProviderFileTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("invalid input must not send an HTTP request")
	}), "sk-safe", "", time.Now().UTC())
	_, err = client.Upload(context.Background(), ProviderFileUploadRequest{
		Filename: "../secret.txt", Content: strings.NewReader("x"), SizeBytes: 1, ExpiresAfterSeconds: 3600,
	})
	assert.ErrorIs(t, err, ErrOpenAIProviderFileRequest)
	_, err = client.Upload(context.Background(), ProviderFileUploadRequest{
		Filename: "safe.txt", Content: strings.NewReader("x"), SizeBytes: OpenAIProviderFileMaximumUploadBytes + 1, ExpiresAfterSeconds: 3600,
	})
	assert.ErrorIs(t, err, ErrOpenAIProviderFileRequest)
	_, err = client.Retrieve(context.Background(), "file-secret?query")
	assert.ErrorIs(t, err, ErrOpenAIProviderFileRequest)
}

func TestProviderFileClientDoesNotFollowRedirects(t *testing.T) {
	fixedNow := time.Unix(2_000_000_000, 0).UTC()
	requestCount := 0
	client := newProviderFileTestClient(t, http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		requestCount++
		responseWriter.Header().Set("Location", "https://example.invalid/credential-capture")
		responseWriter.WriteHeader(http.StatusTemporaryRedirect)
	}), "sk-redirect-secret", "", fixedNow)

	_, err := client.Retrieve(context.Background(), "file-safe")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOpenAIProviderFileUpstreamStatus)
	var statusError ProviderFileUpstreamStatusError
	require.ErrorAs(t, err, &statusError)
	assert.Equal(t, http.StatusTemporaryRedirect, statusError.StatusCode)
	assert.Equal(t, 1, requestCount)
}

func TestProviderFileClientBoundsAndRedactsUpstreamFailures(t *testing.T) {
	fixedNow := time.Unix(2_000_000_000, 0).UTC()
	apiKey := "sk-never-leak"
	sensitiveBody := "upstream-sensitive-body"
	handler := http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		responseWriter.WriteHeader(http.StatusUnauthorized)
		_, _ = responseWriter.Write([]byte(sensitiveBody + request.URL.String()))
	})
	client := newProviderFileTestClient(t, handler, apiKey, "", fixedNow)
	_, err := client.Retrieve(context.Background(), "file-safe")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOpenAIProviderFileUpstreamStatus)
	assert.NotContains(t, err.Error(), sensitiveBody)
	assert.NotContains(t, err.Error(), apiKey)
	assert.NotContains(t, err.Error(), "/v1/files")

	oversizedHandler := http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(strings.Repeat("x", openAIProviderFileMaximumResponseBytes+1)))
	})
	client = newProviderFileTestClient(t, oversizedHandler, apiKey, "", fixedNow)
	_, err = client.Retrieve(context.Background(), "file-safe")
	assert.ErrorIs(t, err, ErrOpenAIProviderFileResponseTooLarge)

	transportSecret := "https://api.openai.com/v1/files/file-safe?key=" + apiKey
	transportClient, constructorErr := NewProviderFileClient(&http.Client{Transport: providerFileRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New(transportSecret)
	})}, OpenAIProviderFileOrigin, apiKey, "")
	require.NoError(t, constructorErr)
	_, err = transportClient.Retrieve(context.Background(), "file-safe")
	assert.ErrorIs(t, err, ErrOpenAIProviderFileTransport)
	assert.NotContains(t, err.Error(), transportSecret)
	assert.NotContains(t, err.Error(), apiKey)
}

func TestProviderFileClientFailsClosedOnInvalidMetadata(t *testing.T) {
	fixedNow := time.Unix(2_000_000_000, 0).UTC()
	tests := []struct {
		name     string
		response map[string]any
	}{
		{name: "mismatched id", response: map[string]any{"id": "file-other", "object": "file", "bytes": 1, "created_at": fixedNow.Unix() - 1, "expires_at": fixedNow.Unix() + 60, "filename": "a.txt", "purpose": "user_data"}},
		{name: "wrong object", response: map[string]any{"id": "file-safe", "object": "other", "bytes": 1, "created_at": fixedNow.Unix() - 1, "expires_at": fixedNow.Unix() + 60, "filename": "a.txt", "purpose": "user_data"}},
		{name: "wrong purpose", response: map[string]any{"id": "file-safe", "object": "file", "bytes": 1, "created_at": fixedNow.Unix() - 1, "expires_at": fixedNow.Unix() + 60, "filename": "a.txt", "purpose": "assistants"}},
		{name: "empty bytes", response: map[string]any{"id": "file-safe", "object": "file", "bytes": 0, "created_at": fixedNow.Unix() - 1, "expires_at": fixedNow.Unix() + 60, "filename": "a.txt", "purpose": "user_data"}},
		{name: "expired", response: map[string]any{"id": "file-safe", "object": "file", "bytes": 1, "created_at": fixedNow.Unix() - 60, "expires_at": fixedNow.Unix(), "filename": "a.txt", "purpose": "user_data"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			responseBody := providerFileResponseBody(t, test.response)
			client := newProviderFileTestClient(t, http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
				responseWriter.Header().Set("Content-Type", "application/json")
				_, _ = responseWriter.Write(responseBody)
			}), "sk-safe", "", fixedNow)
			_, err := client.Retrieve(context.Background(), "file-safe")
			assert.ErrorIs(t, err, ErrOpenAIProviderFileResponse)
		})
	}
}
