//go:build openai_files_sandbox

package openai

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

const (
	openAIProviderFileSandboxConfirmation       = "CREATE_AND_DELETE_ISOLATED_OPENAI_FILES_V1"
	openAIProviderFileSandboxEnvironment        = "isolated_sandbox"
	openAIProviderFileSandboxEvidenceKind       = "openai-files-sandbox-live-observation-v1"
	openAIProviderFileSandboxEvidenceHMACDomain = "openai-files-sandbox-live-observation-v1"
)

var openAIProviderFileSandboxHMACPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var openAIProviderFileSandboxRevisionPattern = regexp.MustCompile(`^[0-9a-f]{40,64}$`)

type openAIProviderFileSandboxConfig struct {
	PrimaryKey            string
	PrimaryOrganization   string
	PrimaryProject        string
	SecondaryKey          string
	SecondaryOrganization string
	SecondaryProject      string
	ExpirationSeconds     int
	EvidenceHMACKey       []byte
	PrimaryProjectHMAC    string
	SecondaryProjectHMAC  string
	EvidencePath          string
}

type openAIProviderFileSandboxOutcome struct {
	Operation string `json:"operation"`
	Result    string `json:"result"`
}

type openAIProviderFileSandboxArtifact struct {
	Kind                    string                             `json:"kind"`
	StartedAt               string                             `json:"started_at"`
	CompletedAt             string                             `json:"completed_at"`
	SourceRevision          string                             `json:"source_revision"`
	PrimaryProjectHMAC      string                             `json:"primary_project_hmac"`
	SecondaryProjectHMAC    string                             `json:"secondary_project_hmac"`
	PrimaryCredentialHMAC   string                             `json:"primary_credential_hmac"`
	SecondaryCredentialHMAC string                             `json:"secondary_credential_hmac"`
	ExpirationSeconds       int                                `json:"expiration_seconds"`
	CorrectProject          []openAIProviderFileSandboxOutcome `json:"correct_project"`
	CrossProject            []openAIProviderFileSandboxOutcome `json:"cross_project"`
	MissingProject          []openAIProviderFileSandboxOutcome `json:"missing_project"`
	Deletion                []openAIProviderFileSandboxOutcome `json:"deletion"`
	Expiry                  []openAIProviderFileSandboxOutcome `json:"expiry"`
	Cleanup                 []openAIProviderFileSandboxOutcome `json:"cleanup"`
	EvidenceHMAC            string                             `json:"evidence_hmac"`
}

func TestOpenAIFilesSandboxContract(t *testing.T) {
	if os.Getenv("TEST_OPENAI_FILES_SANDBOX_CONFIRM") != openAIProviderFileSandboxConfirmation ||
		os.Getenv("TEST_OPENAI_FILES_SANDBOX_ENVIRONMENT") != openAIProviderFileSandboxEnvironment {
		t.Skip("OpenAI Files live observation requires the isolated sandbox confirmation gate")
	}
	config, err := loadOpenAIProviderFileSandboxConfig()
	require.NoError(t, err)
	evidenceFile, err := reserveOpenAIProviderFileSandboxEvidence(config.EvidencePath)
	require.NoError(t, err)
	defer evidenceFile.Close()

	artifact, runErr := runOpenAIProviderFileSandboxObservation(config)
	artifact.CompletedAt = time.Now().UTC().Format(time.RFC3339)
	if runErr != nil {
		artifact.Cleanup = append(artifact.Cleanup, openAIProviderFileSandboxOutcome{Operation: "observation", Result: "failed"})
	}
	artifact.EvidenceHMAC = ""
	unsignedArtifact, marshalErr := common.Marshal(artifact)
	require.NoError(t, marshalErr)
	artifact.EvidenceHMAC = deriveOpenAIProviderFileSandboxHMAC(config.EvidenceHMACKey, "artifact", string(unsignedArtifact))
	artifactBytes, marshalErr := common.Marshal(artifact)
	require.NoError(t, marshalErr)
	_, writeErr := evidenceFile.Write(artifactBytes)
	require.NoError(t, writeErr)
	require.NoError(t, evidenceFile.Sync())
	require.NoError(t, runErr)
}

func loadOpenAIProviderFileSandboxConfig() (openAIProviderFileSandboxConfig, error) {
	config := openAIProviderFileSandboxConfig{
		PrimaryKey:            os.Getenv("TEST_OPENAI_FILES_SANDBOX_PRIMARY_API_KEY"),
		PrimaryOrganization:   os.Getenv("TEST_OPENAI_FILES_SANDBOX_PRIMARY_ORGANIZATION"),
		PrimaryProject:        os.Getenv("TEST_OPENAI_FILES_SANDBOX_PRIMARY_PROJECT"),
		SecondaryKey:          os.Getenv("TEST_OPENAI_FILES_SANDBOX_SECONDARY_API_KEY"),
		SecondaryOrganization: os.Getenv("TEST_OPENAI_FILES_SANDBOX_SECONDARY_ORGANIZATION"),
		SecondaryProject:      os.Getenv("TEST_OPENAI_FILES_SANDBOX_SECONDARY_PROJECT"),
		EvidenceHMACKey:       []byte(os.Getenv("TEST_OPENAI_FILES_SANDBOX_EVIDENCE_HMAC_KEY")),
		PrimaryProjectHMAC:    os.Getenv("TEST_OPENAI_FILES_SANDBOX_PRIMARY_PROJECT_HMAC"),
		SecondaryProjectHMAC:  os.Getenv("TEST_OPENAI_FILES_SANDBOX_SECONDARY_PROJECT_HMAC"),
		EvidencePath:          os.Getenv("TEST_OPENAI_FILES_SANDBOX_EVIDENCE_PATH"),
	}
	if config.PrimaryKey == "" || config.SecondaryKey == "" || config.PrimaryProject == "" || config.SecondaryProject == "" ||
		config.PrimaryProject == config.SecondaryProject || config.PrimaryOrganization != config.SecondaryOrganization || len(config.EvidenceHMACKey) < 32 ||
		bytes.Equal(config.EvidenceHMACKey, []byte(config.PrimaryKey)) || bytes.Equal(config.EvidenceHMACKey, []byte(config.SecondaryKey)) ||
		!openAIProviderFileSandboxHMACPattern.MatchString(config.PrimaryProjectHMAC) ||
		!openAIProviderFileSandboxHMACPattern.MatchString(config.SecondaryProjectHMAC) ||
		config.PrimaryProjectHMAC == config.SecondaryProjectHMAC || filepath.IsAbs(config.EvidencePath) == false {
		return config, errors.New("OpenAI Files sandbox inputs are incomplete or not isolated")
	}
	if os.Getenv("HTTP_PROXY") != "" || os.Getenv("HTTPS_PROXY") != "" || os.Getenv("ALL_PROXY") != "" ||
		os.Getenv("http_proxy") != "" || os.Getenv("https_proxy") != "" || os.Getenv("all_proxy") != "" ||
		strings.Contains(strings.ToLower(os.Getenv("GODEBUG")), "http2debug") {
		return config, errors.New("OpenAI Files sandbox refuses proxy and HTTP debug environments")
	}
	config.ExpirationSeconds = OpenAIProviderFileMinimumExpirySeconds
	if deriveOpenAIProviderFileSandboxHMAC(config.EvidenceHMACKey, "project", config.PrimaryProject) != config.PrimaryProjectHMAC ||
		deriveOpenAIProviderFileSandboxHMAC(config.EvidenceHMACKey, "project", config.SecondaryProject) != config.SecondaryProjectHMAC {
		return config, errors.New("OpenAI Files sandbox project bindings do not match")
	}
	return config, nil
}

func reserveOpenAIProviderFileSandboxEvidence(path string) (*os.File, error) {
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("OpenAI Files sandbox evidence directory is unavailable")
	}
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

func runOpenAIProviderFileSandboxObservation(config openAIProviderFileSandboxConfig) (*openAIProviderFileSandboxArtifact, error) {
	sourceRevision, sourceErr := openAIProviderFileSandboxSourceRevision()
	artifact := &openAIProviderFileSandboxArtifact{
		Kind: openAIProviderFileSandboxEvidenceKind, StartedAt: time.Now().UTC().Format(time.RFC3339), SourceRevision: sourceRevision,
		PrimaryProjectHMAC: config.PrimaryProjectHMAC, SecondaryProjectHMAC: config.SecondaryProjectHMAC,
		PrimaryCredentialHMAC:   deriveOpenAIProviderFileSandboxHMAC(config.EvidenceHMACKey, "credential", config.PrimaryKey),
		SecondaryCredentialHMAC: deriveOpenAIProviderFileSandboxHMAC(config.EvidenceHMACKey, "credential", config.SecondaryKey),
		ExpirationSeconds:       config.ExpirationSeconds,
	}
	if sourceErr != nil {
		return artifact, sourceErr
	}
	if artifact.PrimaryCredentialHMAC == artifact.SecondaryCredentialHMAC {
		return artifact, errors.New("OpenAI Files sandbox credentials are not independent")
	}
	primaryClient, secondaryClient, primaryWrongProjectClient, secondaryWrongCredentialClient, err := newOpenAIProviderFileSandboxClients(config)
	if err != nil {
		return artifact, err
	}
	primaryContext, primaryCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer primaryCancel()
	secondaryContext, secondaryCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer secondaryCancel()
	primaryBaseline, err := listOpenAIProviderFileSandboxAll(primaryContext, primaryClient)
	if err != nil || len(primaryBaseline) != 0 {
		return artifact, errors.New("primary sandbox project is not an empty dedicated target")
	}
	secondaryBaseline, err := listOpenAIProviderFileSandboxAll(secondaryContext, secondaryClient)
	if err != nil || len(secondaryBaseline) != 0 {
		return artifact, errors.New("secondary sandbox project is not an empty dedicated target")
	}
	artifact.CorrectProject = append(artifact.CorrectProject,
		openAIProviderFileSandboxOutcome{Operation: "dedicated_project_preflight", Result: "both_empty"})
	primaryID := ""
	secondaryID := ""
	primaryExpiresAt := time.Time{}
	secondaryExpiresAt := time.Time{}
	primaryCleanupAllowed := true
	secondaryCleanupAllowed := true
	cleanup := func() {
		if primaryID != "" && primaryCleanupAllowed {
			artifact.Cleanup = append(artifact.Cleanup, openAIProviderFileSandboxOutcome{Operation: "primary_delete",
				Result: cleanupOpenAIProviderFileSandboxObject(config.PrimaryKey, config.PrimaryOrganization, config.PrimaryProject,
					primaryClient, primaryID, primaryExpiresAt)})
		}
		if secondaryID != "" && secondaryCleanupAllowed {
			artifact.Cleanup = append(artifact.Cleanup, openAIProviderFileSandboxOutcome{Operation: "secondary_delete",
				Result: cleanupOpenAIProviderFileSandboxObject(config.SecondaryKey, config.SecondaryOrganization, config.SecondaryProject,
					secondaryClient, secondaryID, secondaryExpiresAt)})
		}
	}
	defer cleanup()
	primaryFile, err := primaryClient.Upload(primaryContext, ProviderFileUploadRequest{Filename: "sandbox-primary.txt", Content: bytes.NewReader([]byte("primary")), SizeBytes: 7, ExpiresAfterSeconds: config.ExpirationSeconds})
	if err != nil {
		recoveredFile, recoveryResult := reconcileOpenAIProviderFileSandboxUnknownUpload(primaryClient, "sandbox-primary.txt", 7)
		primaryID = recoveredFile.ProviderFileID
		primaryExpiresAt = time.Unix(recoveredFile.ExpiresAtUnix, 0).UTC()
		artifact.Cleanup = append(artifact.Cleanup,
			openAIProviderFileSandboxOutcome{Operation: "primary_upload_reconciliation", Result: recoveryResult})
		return artifact, errors.New("primary sandbox upload failed")
	}
	primaryID = primaryFile.ProviderFileID
	primaryExpiresAt = time.Unix(primaryFile.ExpiresAtUnix, 0).UTC()
	secondaryFile, err := secondaryClient.Upload(secondaryContext, ProviderFileUploadRequest{Filename: "sandbox-secondary.txt", Content: bytes.NewReader([]byte("secondary")), SizeBytes: 9, ExpiresAfterSeconds: config.ExpirationSeconds})
	if err != nil {
		recoveredFile, recoveryResult := reconcileOpenAIProviderFileSandboxUnknownUpload(secondaryClient, "sandbox-secondary.txt", 9)
		secondaryID = recoveredFile.ProviderFileID
		secondaryExpiresAt = time.Unix(recoveredFile.ExpiresAtUnix, 0).UTC()
		artifact.Cleanup = append(artifact.Cleanup,
			openAIProviderFileSandboxOutcome{Operation: "secondary_upload_reconciliation", Result: recoveryResult})
		return artifact, errors.New("secondary sandbox upload failed")
	}
	secondaryID = secondaryFile.ProviderFileID
	secondaryExpiresAt = time.Unix(secondaryFile.ExpiresAtUnix, 0).UTC()
	if primaryFile.ExpiresAtUnix-primaryFile.CreatedAtUnix != int64(config.ExpirationSeconds) ||
		secondaryFile.ExpiresAtUnix-secondaryFile.CreatedAtUnix != int64(config.ExpirationSeconds) {
		return artifact, errors.New("sandbox upload expiration did not match the requested policy")
	}
	artifact.CorrectProject = append(artifact.CorrectProject,
		openAIProviderFileSandboxOutcome{Operation: "upload_expiration", Result: "matches_requested_seconds"})
	if metadata, retrieveErr := primaryClient.Retrieve(primaryContext, primaryID); retrieveErr != nil || metadata.ProviderFileID != primaryID {
		return artifact, errors.New("primary project retrieve failed")
	}
	primaryFiles, err := listOpenAIProviderFileSandboxAll(primaryContext, primaryClient)
	if err != nil || !openAIProviderFileSandboxListContains(primaryFiles, primaryID) {
		return artifact, errors.New("primary project list failed")
	}
	artifact.CorrectProject = append(artifact.CorrectProject,
		openAIProviderFileSandboxOutcome{Operation: "primary_retrieve", Result: "success"},
		openAIProviderFileSandboxOutcome{Operation: "primary_list", Result: "contains_file"})
	if metadata, retrieveErr := secondaryClient.Retrieve(secondaryContext, secondaryID); retrieveErr != nil || metadata.ProviderFileID != secondaryID {
		return artifact, errors.New("secondary project retrieve failed")
	}
	secondaryFiles, err := listOpenAIProviderFileSandboxAll(secondaryContext, secondaryClient)
	if err != nil || !openAIProviderFileSandboxListContains(secondaryFiles, secondaryID) {
		return artifact, errors.New("secondary project list failed")
	}
	artifact.CorrectProject = append(artifact.CorrectProject,
		openAIProviderFileSandboxOutcome{Operation: "secondary_retrieve", Result: "success"},
		openAIProviderFileSandboxOutcome{Operation: "secondary_list", Result: "contains_file"})
	if metadata, retrieveErr := primaryWrongProjectClient.Retrieve(primaryContext, primaryID); retrieveErr == nil && metadata.ProviderFileID == primaryID {
		return artifact, errors.New("primary credential retrieved its file through the wrong project")
	} else {
		artifact.CrossProject = append(artifact.CrossProject,
			openAIProviderFileSandboxOutcome{Operation: "primary_credential_wrong_project", Result: classifyOpenAIProviderFileSandboxError(retrieveErr)})
	}
	if metadata, retrieveErr := secondaryWrongCredentialClient.Retrieve(primaryContext, primaryID); retrieveErr == nil && metadata.ProviderFileID == primaryID {
		return artifact, errors.New("wrong credential retrieved the primary project file")
	} else {
		artifact.CrossProject = append(artifact.CrossProject,
			openAIProviderFileSandboxOutcome{Operation: "primary_project_wrong_credential", Result: classifyOpenAIProviderFileSandboxError(retrieveErr)})
	}
	if metadata, retrieveErr := secondaryClient.Retrieve(primaryContext, primaryID); retrieveErr == nil && metadata.ProviderFileID == primaryID {
		return artifact, errors.New("secondary project retrieved primary file")
	} else {
		artifact.CrossProject = append(artifact.CrossProject, openAIProviderFileSandboxOutcome{Operation: "secondary_retrieve_primary", Result: classifyOpenAIProviderFileSandboxError(retrieveErr)})
	}
	if metadata, retrieveErr := primaryClient.Retrieve(secondaryContext, secondaryID); retrieveErr == nil && metadata.ProviderFileID == secondaryID {
		return artifact, errors.New("primary project retrieved secondary file")
	} else {
		artifact.CrossProject = append(artifact.CrossProject, openAIProviderFileSandboxOutcome{Operation: "primary_retrieve_secondary", Result: classifyOpenAIProviderFileSandboxError(retrieveErr)})
	}
	if openAIProviderFileSandboxListContains(secondaryFiles, primaryID) || openAIProviderFileSandboxListContains(primaryFiles, secondaryID) {
		return artifact, errors.New("cross-project list isolation failed")
	}
	artifact.CrossProject = append(artifact.CrossProject,
		openAIProviderFileSandboxOutcome{Operation: "bilateral_list", Result: "isolated"})
	if status, requestErr := openAIProviderFileSandboxRequestStatus(primaryContext, config.PrimaryKey, config.PrimaryOrganization, "", "/v1/files/"+primaryID); requestErr != nil {
		return artifact, errors.New("missing project request observation failed")
	} else {
		artifact.MissingProject = append(artifact.MissingProject,
			openAIProviderFileSandboxOutcome{Operation: "retrieve_without_project", Result: fmt.Sprintf("http_%d", status)})
	}
	if result, requestErr := openAIProviderFileSandboxMissingProjectListObservation(primaryContext, config, primaryID); requestErr != nil {
		return artifact, errors.New("missing project list observation failed")
	} else {
		artifact.MissingProject = append(artifact.MissingProject,
			openAIProviderFileSandboxOutcome{Operation: "list_without_project", Result: result})
	}
	if deleteErr := primaryClient.Delete(primaryContext, primaryID); deleteErr != nil {
		deleteResult := classifyOpenAIProviderFileSandboxError(deleteErr)
		if deleteResult != string(ProviderFileDeleteFailureRateLimited) {
			primaryCleanupAllowed = false
			unknownDeleteResult, unknownDeleteErr := confirmOpenAIProviderFileSandboxAbsence(config.PrimaryKey, config.PrimaryOrganization,
				config.PrimaryProject, primaryClient, primaryID, time.Now().UTC(), time.Unix(primaryFile.ExpiresAtUnix, 0).UTC().Add(5*time.Minute))
			cleanupResult := "pending_manual_review_after_" + deleteResult + "_observed_" + unknownDeleteResult
			if unknownDeleteErr == nil {
				cleanupResult = "confirmed_absent_after_" + deleteResult + "_observed_" + unknownDeleteResult
			}
			artifact.Cleanup = append(artifact.Cleanup,
				openAIProviderFileSandboxOutcome{Operation: "primary_delete", Result: cleanupResult})
		}
		return artifact, errors.New("primary project delete failed")
	}
	primaryCleanupAllowed = false
	artifact.Cleanup = append(artifact.Cleanup,
		openAIProviderFileSandboxOutcome{Operation: "primary_delete", Result: "confirmed_by_contract_step"})
	artifact.Deletion = append(artifact.Deletion, openAIProviderFileSandboxOutcome{Operation: "first_delete", Result: "deleted"})
	if deleteErr := primaryClient.Delete(primaryContext, primaryID); deleteErr != nil {
		artifact.Deletion = append(artifact.Deletion, openAIProviderFileSandboxOutcome{Operation: "repeat_delete", Result: classifyOpenAIProviderFileSandboxError(deleteErr)})
	} else {
		artifact.Deletion = append(artifact.Deletion, openAIProviderFileSandboxOutcome{Operation: "repeat_delete", Result: "deleted"})
	}
	deletedAt := time.Now().UTC()
	deleteAbsenceResult, absenceErr := confirmOpenAIProviderFileSandboxAbsence(config.PrimaryKey, config.PrimaryOrganization, config.PrimaryProject,
		primaryClient, primaryID, deletedAt, deletedAt.Add(time.Minute))
	deleteListResult := "absent_twice"
	if absenceErr != nil {
		deleteListResult = "not_confirmed"
	}
	artifact.Deletion = append(artifact.Deletion,
		openAIProviderFileSandboxOutcome{Operation: "post_delete_retrieve", Result: deleteAbsenceResult},
		openAIProviderFileSandboxOutcome{Operation: "post_delete_list", Result: deleteListResult})
	if absenceErr != nil {
		return artifact, errors.New("deleted file absence was not confirmed")
	}
	expiryErr := observeOpenAIProviderFileSandboxExpiry(config, secondaryClient, secondaryID,
		time.Unix(secondaryFile.ExpiresAtUnix, 0).UTC(), artifact)
	if expiryErr == nil {
		secondaryCleanupAllowed = false
		artifact.Cleanup = append(artifact.Cleanup,
			openAIProviderFileSandboxOutcome{Operation: "secondary_delete", Result: "confirmed_by_expiry"})
	}
	return artifact, expiryErr
}

func openAIProviderFileSandboxSourceRevision() (string, error) {
	buildInfo, ok := debug.ReadBuildInfo()
	if !ok {
		return "", errors.New("OpenAI Files sandbox source identity is unavailable")
	}
	revision := ""
	modified := false
	for _, setting := range buildInfo.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if modified || !openAIProviderFileSandboxRevisionPattern.MatchString(revision) {
		return "", errors.New("OpenAI Files sandbox requires a clean source revision")
	}
	return revision, nil
}

func cleanupOpenAIProviderFileSandboxObject(apiKey, organization, project string, client *ProviderFileClient,
	providerFileID string, expiresAt time.Time) string {
	deleteResult := "not_attempted"
	for attempt := 0; attempt < 3; attempt++ {
		deleteContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		deleteErr := client.Delete(deleteContext, providerFileID)
		cancel()
		if deleteErr == nil {
			return "deleted"
		}
		deleteResult = classifyOpenAIProviderFileSandboxError(deleteErr)
		if deleteResult != string(ProviderFileDeleteFailureRateLimited) {
			break
		}
		time.Sleep(5 * time.Second)
	}
	deadline := expiresAt.Add(5 * time.Minute)
	minimumDeadline := time.Now().Add(30 * time.Second)
	if deadline.Before(minimumDeadline) {
		deadline = minimumDeadline
	}
	absenceResult, err := confirmOpenAIProviderFileSandboxAbsence(apiKey, organization, project, client,
		providerFileID, time.Now().UTC(), deadline)
	if err != nil {
		return "manual_review_after_" + deleteResult + "_observed_" + absenceResult
	}
	return "confirmed_absent_after_" + deleteResult + "_observed_" + absenceResult
}

func reconcileOpenAIProviderFileSandboxUnknownUpload(client *ProviderFileClient, filename string, sizeBytes int64) (ProviderFileMetadata, string) {
	startedAt := time.Now()
	deadline := startedAt.Add(2 * time.Minute)
	emptyConfirmations := 0
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		files, err := listOpenAIProviderFileSandboxAll(ctx, client)
		cancel()
		if err == nil {
			if len(files) == 0 {
				if time.Now().After(startedAt.Add(75 * time.Second)) {
					emptyConfirmations++
					if emptyConfirmations >= 2 {
						return ProviderFileMetadata{}, "project_empty_after_expiry_window"
					}
				}
				time.Sleep(5 * time.Second)
				continue
			}
			emptyConfirmations = 0
			if len(files) == 1 && files[0].Filename == filename && files[0].Bytes == sizeBytes {
				return files[0], "recovered_for_cleanup"
			}
			return ProviderFileMetadata{}, "ambiguous_project_contents"
		}
		time.Sleep(5 * time.Second)
	}
	return ProviderFileMetadata{}, "not_reconciled_before_deadline"
}

func newOpenAIProviderFileSandboxClients(config openAIProviderFileSandboxConfig) (*ProviderFileClient, *ProviderFileClient, *ProviderFileClient, *ProviderFileClient, error) {
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second}
	httpClient := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	primaryClient, err := NewProviderFileClient(httpClient, OpenAIProviderFileOrigin, config.PrimaryKey, config.PrimaryOrganization, config.PrimaryProject)
	if err != nil {
		return nil, nil, nil, nil, errors.New("primary sandbox client configuration failed")
	}
	secondaryClient, err := NewProviderFileClient(httpClient, OpenAIProviderFileOrigin, config.SecondaryKey, config.SecondaryOrganization, config.SecondaryProject)
	if err != nil {
		return nil, nil, nil, nil, errors.New("secondary sandbox client configuration failed")
	}
	primaryWrongProjectClient, err := NewProviderFileClient(httpClient, OpenAIProviderFileOrigin, config.PrimaryKey, config.PrimaryOrganization, config.SecondaryProject)
	if err != nil {
		return nil, nil, nil, nil, errors.New("wrong project sandbox client configuration failed")
	}
	secondaryWrongCredentialClient, err := NewProviderFileClient(httpClient, OpenAIProviderFileOrigin, config.SecondaryKey, config.PrimaryOrganization, config.PrimaryProject)
	if err != nil {
		return nil, nil, nil, nil, errors.New("wrong credential sandbox client configuration failed")
	}
	return primaryClient, secondaryClient, primaryWrongProjectClient, secondaryWrongCredentialClient, nil
}

func openAIProviderFileSandboxRequestStatus(ctx context.Context, apiKey, organization, project, path string) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, OpenAIProviderFileOrigin+path, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	if organization != "" {
		request.Header.Set("OpenAI-Organization", organization)
	}
	if project != "" {
		request.Header.Set("OpenAI-Project", project)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}, Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	return response.StatusCode, nil
}

func openAIProviderFileSandboxMissingProjectListObservation(ctx context.Context, config openAIProviderFileSandboxConfig, primaryID string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, OpenAIProviderFileOrigin+"/v1/files?purpose=user_data&order=asc&limit=100", nil)
	if err != nil {
		return "request_invalid", err
	}
	request.Header.Set("Authorization", "Bearer "+config.PrimaryKey)
	if config.PrimaryOrganization != "" {
		request.Header.Set("OpenAI-Organization", config.PrimaryOrganization)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}, Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return "transport_or_timeout", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Sprintf("http_%d", response.StatusCode), nil
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, openAIProviderFileMaximumListBytes+1))
	if err != nil || len(responseBody) > openAIProviderFileMaximumListBytes {
		return "invalid_response", errors.New("missing project list response is invalid")
	}
	var page providerFileWireListPage
	if err := common.Unmarshal(responseBody, &page); err != nil || page.Data == nil {
		return "invalid_response", errors.New("missing project list response is invalid")
	}
	for _, file := range page.Data {
		if file.ID == primaryID {
			return "http_200_contains_primary", nil
		}
	}
	return "http_200_primary_absent", nil
}

func listOpenAIProviderFileSandboxAll(ctx context.Context, client *ProviderFileClient) ([]ProviderFileMetadata, error) {
	var allFiles []ProviderFileMetadata
	after := ""
	for pageNumber := 0; pageNumber < 100; pageNumber++ {
		page, err := client.List(ctx, after)
		if err != nil {
			return nil, err
		}
		allFiles = append(allFiles, page.Files...)
		if !page.HasMore {
			return allFiles, nil
		}
		if page.LastID == "" || page.LastID == after {
			return nil, errors.New("sandbox list cursor did not advance")
		}
		after = page.LastID
	}
	return nil, errors.New("sandbox list page cap exceeded")
}

func openAIProviderFileSandboxListContains(files []ProviderFileMetadata, providerFileID string) bool {
	for _, file := range files {
		if file.ProviderFileID == providerFileID {
			return true
		}
	}
	return false
}

func observeOpenAIProviderFileSandboxExpiry(config openAIProviderFileSandboxConfig, client *ProviderFileClient, providerFileID string, expiresAt time.Time, artifact *openAIProviderFileSandboxArtifact) error {
	expiryAbsenceResult, err := confirmOpenAIProviderFileSandboxAbsence(config.SecondaryKey, config.SecondaryOrganization, config.SecondaryProject,
		client, providerFileID, expiresAt, expiresAt.Add(5*time.Minute))
	expiryListResult := "absent_twice"
	if err != nil {
		expiryListResult = "not_confirmed"
	}
	artifact.Expiry = append(artifact.Expiry,
		openAIProviderFileSandboxOutcome{Operation: "retrieve_after_expiry_wait", Result: expiryAbsenceResult},
		openAIProviderFileSandboxOutcome{Operation: "list_after_expiry_wait", Result: expiryListResult})
	if err != nil {
		return errors.New("file expiry was not observed within the bounded window")
	}
	return nil
}

func confirmOpenAIProviderFileSandboxAbsence(apiKey, organization, project string, client *ProviderFileClient,
	providerFileID string, notBefore, deadline time.Time) (string, error) {
	confirmedAbsent := 0
	confirmedStatus := 0
	lastResult := "not_observed"
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		status, retrieveErr := openAIProviderFileSandboxRequestStatus(ctx, apiKey, organization, project, "/v1/files/"+providerFileID)
		cancel()
		if retrieveErr != nil {
			lastResult = "transport_or_timeout"
		} else {
			lastResult = fmt.Sprintf("http_%d", status)
		}
		terminalStatus := status >= 400 && status < 500 && status != http.StatusUnauthorized && status != http.StatusForbidden &&
			status != http.StatusRequestTimeout && status != http.StatusTooManyRequests
		if !time.Now().Before(notBefore) && retrieveErr == nil && terminalStatus {
			listContext, listCancel := context.WithTimeout(context.Background(), 30*time.Second)
			files, listErr := listOpenAIProviderFileSandboxAll(listContext, client)
			listCancel()
			if listErr == nil && !openAIProviderFileSandboxListContains(files, providerFileID) {
				if confirmedStatus == status {
					confirmedAbsent++
				} else {
					confirmedStatus = status
					confirmedAbsent = 1
				}
				if confirmedAbsent >= 2 {
					return fmt.Sprintf("http_%d_twice", status), nil
				}
			} else {
				confirmedAbsent = 0
			}
		} else {
			confirmedAbsent = 0
		}
		time.Sleep(10 * time.Second)
	}
	return lastResult, errors.New("file absence was not confirmed within the bounded window")
}

func classifyOpenAIProviderFileSandboxError(err error) string {
	if err == nil {
		return "success"
	}
	var statusErr ProviderFileUpstreamStatusError
	if errors.As(err, &statusErr) {
		return fmt.Sprintf("http_%d", statusErr.StatusCode)
	}
	var deleteErr ProviderFileDeleteError
	if errors.As(err, &deleteErr) {
		return string(deleteErr.Code)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrOpenAIProviderFileTransport) {
		return "transport_or_timeout"
	}
	return "client_error"
}

func deriveOpenAIProviderFileSandboxHMAC(key []byte, domain, value string) string {
	hash := hmac.New(sha256.New, key)
	_, _ = hash.Write([]byte(openAIProviderFileSandboxEvidenceHMACDomain + "\x00" + domain + "\x00" + value))
	return hex.EncodeToString(hash.Sum(nil))
}
