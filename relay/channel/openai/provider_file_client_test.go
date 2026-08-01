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

func newProviderFileTestClient(t *testing.T, handler http.Handler, apiKey, organization, project string, now time.Time) *ProviderFileClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	httpClient := server.Client()
	httpClient.Transport = providerFileRewriteTransport{target: target, base: httpClient.Transport}
	client, err := NewProviderFileClient(httpClient, OpenAIProviderFileOrigin, apiKey, organization, project)
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
	project := "proj-provider-file"
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
		assert.Equal(t, project, request.Header.Get("OpenAI-Project"))
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
	client := newProviderFileTestClient(t, handler, apiKey, organization, project, fixedNow)

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
	for _, formattedClient := range []string{fmt.Sprintf("%v", client), fmt.Sprintf("%#v", client)} {
		for _, sensitiveValue := range []string{apiKey, organization, project, OpenAIProviderFileOrigin} {
			assert.NotContains(t, formattedClient, sensitiveValue)
		}
	}
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
		assert.Empty(t, request.Header.Get("OpenAI-Project"))
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write(responseBody)
	})
	client := newProviderFileTestClient(t, handler, "sk-retrieve", "", "", fixedNow)

	metadata, err := client.Retrieve(context.Background(), providerFileID)
	require.NoError(t, err)
	assert.Equal(t, providerFileID, metadata.ProviderFileID)
	assert.Equal(t, OpenAIProviderFilePurposeUserData, metadata.Purpose)
}

func TestProviderFileClientDeleteRequiresExactSuccessReceipt(t *testing.T) {
	providerFileID := "file-delete_1"
	apiKey := "sk-delete-secret"
	organization := "org-delete"
	project := "proj-delete"
	requestCount := 0
	responseBody := providerFileResponseBody(t, map[string]any{
		"id": providerFileID, "object": "file", "deleted": true,
	})
	client := newProviderFileTestClient(t, http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		requestCount++
		assert.Equal(t, http.MethodDelete, request.Method)
		assert.Equal(t, "/v1/files/"+providerFileID, request.URL.Path)
		assert.Equal(t, "Bearer "+apiKey, request.Header.Get("Authorization"))
		assert.Equal(t, organization, request.Header.Get("OpenAI-Organization"))
		assert.Equal(t, project, request.Header.Get("OpenAI-Project"))
		responseWriter.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = responseWriter.Write(responseBody)
	}), apiKey, organization, project, time.Now().UTC())

	require.NoError(t, client.Delete(context.Background(), providerFileID))
	assert.Equal(t, 1, requestCount)
}

func TestProviderFileClientDeleteClassifiesStatusWithoutRetryOrLeak(t *testing.T) {
	providerFileID := "file-delete-secret"
	apiKey := "sk-delete-never-leak"
	tests := []struct {
		name         string
		statusCode   int
		location     string
		expectedCode ProviderFileDeleteFailureCode
	}{
		{name: "not found remains failure", statusCode: http.StatusNotFound, expectedCode: ProviderFileDeleteFailureNotFound},
		{name: "rate limited", statusCode: http.StatusTooManyRequests, expectedCode: ProviderFileDeleteFailureRateLimited},
		{name: "server error", statusCode: http.StatusServiceUnavailable, expectedCode: ProviderFileDeleteFailureUpstreamServer},
		{name: "authentication", statusCode: http.StatusForbidden, expectedCode: ProviderFileDeleteFailureAuthentication},
		{name: "unexpected", statusCode: http.StatusConflict, expectedCode: ProviderFileDeleteFailureUnexpectedStatus},
		{name: "redirect is not followed", statusCode: http.StatusTemporaryRedirect, location: "https://example.invalid/credential-capture", expectedCode: ProviderFileDeleteFailureUnexpectedStatus},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestCount := 0
			sensitiveBody := "sensitive-delete-body:" + providerFileID + ":" + apiKey
			client := newProviderFileTestClient(t, http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
				requestCount++
				responseWriter.Header().Set("Content-Type", "application/json")
				if test.location != "" {
					responseWriter.Header().Set("Location", test.location)
				}
				responseWriter.WriteHeader(test.statusCode)
				_, _ = responseWriter.Write([]byte(sensitiveBody))
			}), apiKey, "", "proj-delete-status", time.Now().UTC())

			err := client.Delete(context.Background(), providerFileID)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrOpenAIProviderFileDelete)
			var deleteError ProviderFileDeleteError
			require.ErrorAs(t, err, &deleteError)
			assert.Equal(t, test.expectedCode, deleteError.Code)
			assert.Equal(t, test.statusCode, deleteError.StatusCode)
			assert.Equal(t, 1, requestCount)
			assert.NotContains(t, err.Error(), providerFileID)
			assert.NotContains(t, err.Error(), apiKey)
			assert.NotContains(t, err.Error(), sensitiveBody)
			assert.NotContains(t, err.Error(), "/v1/files")
		})
	}
}

func TestProviderFileClientDeleteClassifiesTimeoutAndTransportErrors(t *testing.T) {
	providerFileID := "file-delete-safe"
	apiKey := "sk-delete-transport-secret"
	tests := []struct {
		name         string
		transportErr error
		expectedCode ProviderFileDeleteFailureCode
	}{
		{name: "timeout", transportErr: fmt.Errorf("sensitive timeout detail: %w", context.DeadlineExceeded), expectedCode: ProviderFileDeleteFailureTimeout},
		{name: "transport", transportErr: errors.New("https://api.openai.com/v1/files/file-delete-safe?key=secret"), expectedCode: ProviderFileDeleteFailureTransport},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestCount := 0
			client, err := NewProviderFileClient(&http.Client{Transport: providerFileRoundTripFunc(func(*http.Request) (*http.Response, error) {
				requestCount++
				return nil, test.transportErr
			})}, OpenAIProviderFileOrigin, apiKey, "", "proj-delete-transport")
			require.NoError(t, err)

			err = client.Delete(context.Background(), providerFileID)
			require.Error(t, err)
			var deleteError ProviderFileDeleteError
			require.ErrorAs(t, err, &deleteError)
			assert.Equal(t, test.expectedCode, deleteError.Code)
			assert.Equal(t, 1, requestCount)
			assert.NotContains(t, err.Error(), providerFileID)
			assert.NotContains(t, err.Error(), apiKey)
			assert.NotContains(t, err.Error(), test.transportErr.Error())
		})
	}
}

func TestProviderFileClientDeleteFailsClosedOnMalformedReceipt(t *testing.T) {
	providerFileID := "file-delete-safe"
	tests := []struct {
		name         string
		contentType  string
		responseBody []byte
		expectedCode ProviderFileDeleteFailureCode
	}{
		{name: "invalid json", contentType: "application/json", responseBody: []byte(`{"id":`), expectedCode: ProviderFileDeleteFailureInvalidResponse},
		{name: "wrong content type", contentType: "text/plain", responseBody: []byte(`{"id":"file-delete-safe","object":"file","deleted":true}`), expectedCode: ProviderFileDeleteFailureInvalidResponse},
		{name: "mismatched id", contentType: "application/json", responseBody: []byte(`{"id":"file-other-secret","object":"file","deleted":true}`), expectedCode: ProviderFileDeleteFailureInvalidResponse},
		{name: "wrong object", contentType: "application/json", responseBody: []byte(`{"id":"file-delete-safe","object":"other","deleted":true}`), expectedCode: ProviderFileDeleteFailureInvalidResponse},
		{name: "deleted false", contentType: "application/json", responseBody: []byte(`{"id":"file-delete-safe","object":"file","deleted":false}`), expectedCode: ProviderFileDeleteFailureInvalidResponse},
		{name: "response too large", contentType: "application/json", responseBody: []byte(strings.Repeat("x", openAIProviderFileMaximumResponseBytes+1)), expectedCode: ProviderFileDeleteFailureResponseTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newProviderFileTestClient(t, http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
				responseWriter.Header().Set("Content-Type", test.contentType)
				_, _ = responseWriter.Write(test.responseBody)
			}), "sk-delete-safe", "", "proj-delete-response", time.Now().UTC())

			err := client.Delete(context.Background(), providerFileID)
			require.Error(t, err)
			var deleteError ProviderFileDeleteError
			require.ErrorAs(t, err, &deleteError)
			assert.Equal(t, test.expectedCode, deleteError.Code)
			assert.NotContains(t, err.Error(), providerFileID)
			assert.NotContains(t, err.Error(), "file-other-secret")
		})
	}
}

func TestProviderFileClientRejectsNonOfficialOriginAndUnsafeInputs(t *testing.T) {
	httpClient := &http.Client{}
	for _, endpoint := range []string{
		"http://api.openai.com", "https://api.openai.com.evil.example", "https://api.openai.com:443", "https://api.openai.com/v1", "https://user@api.openai.com",
	} {
		client, err := NewProviderFileClient(httpClient, endpoint, "sk-safe", "", "proj-safe")
		assert.ErrorIs(t, err, ErrOpenAIProviderFileClientConfiguration)
		assert.Nil(t, client)
	}
	client, err := NewProviderFileClient(httpClient, OpenAIProviderFileOrigin, "sk-safe\r\nInjected: true", "", "proj-safe")
	assert.ErrorIs(t, err, ErrOpenAIProviderFileClientConfiguration)
	assert.Nil(t, client)

	client = newProviderFileTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("invalid input must not send an HTTP request")
	}), "sk-safe", "", "proj-safe", time.Now().UTC())
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
	err = client.Delete(context.Background(), "file-secret?query")
	assert.ErrorIs(t, err, ErrOpenAIProviderFileRequest)
}

func TestProviderFileClientAllowsEmptyProjectAndRejectsUnsafeValues(t *testing.T) {
	client, err := NewProviderFileClient(&http.Client{}, OpenAIProviderFileOrigin, "sk-safe", "", "")
	require.NoError(t, err)
	assert.NotNil(t, client)

	tests := []struct {
		name    string
		project string
	}{
		{name: "leading whitespace", project: " proj-secret"},
		{name: "trailing whitespace", project: "proj-secret "},
		{name: "header injection", project: "proj-secret\r\nAuthorization: exposed"},
		{name: "non ascii", project: "项目"},
		{name: "too long", project: strings.Repeat("p", 4097)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewProviderFileClient(&http.Client{}, OpenAIProviderFileOrigin, "sk-safe", "", test.project)
			require.ErrorIs(t, err, ErrOpenAIProviderFileClientConfiguration)
			assert.Nil(t, client)
			assert.Equal(t, ErrOpenAIProviderFileClientConfiguration.Error(), err.Error())
			assert.NotContains(t, err.Error(), test.project)
		})
	}
}

func TestProviderFileClientDoesNotFollowRedirects(t *testing.T) {
	fixedNow := time.Unix(2_000_000_000, 0).UTC()
	requestCount := 0
	client := newProviderFileTestClient(t, http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		requestCount++
		responseWriter.Header().Set("Location", "https://example.invalid/credential-capture")
		responseWriter.WriteHeader(http.StatusTemporaryRedirect)
	}), "sk-redirect-secret", "", "proj-redirect", fixedNow)

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
	organization := "org-never-leak"
	project := "proj-never-leak"
	sensitiveBody := "upstream-sensitive-body"
	handler := http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		responseWriter.WriteHeader(http.StatusUnauthorized)
		_, _ = responseWriter.Write([]byte(sensitiveBody + request.URL.String()))
	})
	client := newProviderFileTestClient(t, handler, apiKey, organization, project, fixedNow)
	_, err := client.Retrieve(context.Background(), "file-safe")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOpenAIProviderFileUpstreamStatus)
	assert.NotContains(t, err.Error(), sensitiveBody)
	assert.NotContains(t, err.Error(), apiKey)
	assert.NotContains(t, err.Error(), organization)
	assert.NotContains(t, err.Error(), project)
	assert.NotContains(t, err.Error(), "/v1/files")

	oversizedHandler := http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(strings.Repeat("x", openAIProviderFileMaximumResponseBytes+1)))
	})
	client = newProviderFileTestClient(t, oversizedHandler, apiKey, organization, project, fixedNow)
	_, err = client.Retrieve(context.Background(), "file-safe")
	assert.ErrorIs(t, err, ErrOpenAIProviderFileResponseTooLarge)
	assert.NotContains(t, err.Error(), organization)
	assert.NotContains(t, err.Error(), project)

	transportSecret := "https://api.openai.com/v1/files/file-safe?key=" + apiKey + "&project=" + project
	transportClient, constructorErr := NewProviderFileClient(&http.Client{Transport: providerFileRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New(transportSecret)
	})}, OpenAIProviderFileOrigin, apiKey, organization, project)
	require.NoError(t, constructorErr)
	_, err = transportClient.Retrieve(context.Background(), "file-safe")
	assert.ErrorIs(t, err, ErrOpenAIProviderFileTransport)
	assert.NotContains(t, err.Error(), transportSecret)
	assert.NotContains(t, err.Error(), apiKey)
	assert.NotContains(t, err.Error(), organization)
	assert.NotContains(t, err.Error(), project)
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
			}), "sk-safe", "", "proj-metadata", fixedNow)
			_, err := client.Retrieve(context.Background(), "file-safe")
			assert.ErrorIs(t, err, ErrOpenAIProviderFileResponse)
		})
	}
}
