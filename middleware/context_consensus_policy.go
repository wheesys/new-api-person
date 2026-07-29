package middleware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
)

const (
	contextIDHeader        = "X-New-Api-Context-Id"
	contextModeHeader      = "X-New-Api-Context-Mode"
	contextRevisionHeader  = "X-New-Api-Context-Revision"
	managedLeaseMaximumTTL = 30 * time.Second
)

func captureContextConsensusPolicy(c *gin.Context) error {
	if c == nil || c.Request == nil {
		return nil
	}

	mode := strings.ToLower(strings.TrimSpace(c.Request.Header.Get(contextModeHeader)))
	contextID := strings.TrimSpace(c.Request.Header.Get(contextIDHeader))
	revisionValue := strings.TrimSpace(c.Request.Header.Get(contextRevisionHeader))

	c.Request.Header.Del(contextIDHeader)
	c.Request.Header.Del(contextModeHeader)
	c.Request.Header.Del(contextRevisionHeader)

	if mode != "" && mode != "full" && mode != "auto_compact" {
		if mode != "managed" {
			return fmt.Errorf("unsupported context mode")
		}
	}

	settings := model_setting.GetSmartRoutingSettings()
	if mode == "managed" {
		if contextID == "" || revisionValue == "" {
			return fmt.Errorf("managed context requires context ID and revision")
		}
		if !settings.ContextConsensusEnabled || !settings.ManagedContextEnabled {
			return fmt.Errorf("managed context is disabled")
		}
		if !common.GetContextKeyBool(c, constant.ContextKeyTokenContextCompaction) {
			return fmt.Errorf("managed context is not authorized for this token")
		}
		expectedRevision, err := strconv.ParseUint(revisionValue, 10, 64)
		if err != nil {
			return fmt.Errorf("managed context revision must be a non-negative integer")
		}
		common.SetContextKey(c, constant.ContextKeyManagedContextRequest, contextconsensus.ManagedContextRequest{
			ExternalContextID: contextID,
			ExpectedRevision:  expectedRevision,
		})
	} else if contextID != "" || revisionValue != "" {
		return fmt.Errorf("context ID and revision require managed context mode")
	}
	policy := contextconsensus.CompactionPolicy{
		SystemEnabled:        settings.ContextConsensusEnabled && settings.AutoCompactionEnabled,
		PolicyVersion:        "context-consensus-v1",
		PreservedRecentTurns: settings.PreservedRecentTurns,
		TargetInputTokens:    settings.MaxCompactionInputTokens,
		MaxSummaryTokens:     settings.MaxSummaryTokens,
	}
	snapshot := policy.Snapshot(
		common.GetContextKeyBool(c, constant.ContextKeyTokenContextCompaction),
		mode == "auto_compact" || mode == "managed",
	)
	common.SetContextKey(c, constant.ContextKeyContextConsensusPolicy, snapshot)
	return nil
}

func prepareManagedConsensusRequest(c *gin.Context) (*contextconsensus.ManagedConsensusLeaseGuard, int, error) {
	managedRequest, managed := common.GetContextKeyType[contextconsensus.ManagedContextRequest](c, constant.ContextKeyManagedContextRequest)
	if !managed {
		return nil, 0, nil
	}
	protocol, endpointFamily, err := managedProtocolAndEndpointFamily(c.Request.URL.Path)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	if managedEndpointStreams(c.Request) {
		return nil, http.StatusBadRequest, fmt.Errorf("managed context does not support streaming requests")
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("read managed request body: %w", err)
	}
	body, err := storage.Bytes()
	if err != nil {
		return nil, http.StatusBadRequest, fmt.Errorf("read managed request body: %w", err)
	}
	if err := contextconsensus.ValidateManagedIncrementalRequest(protocol, body); err != nil {
		return nil, http.StatusBadRequest, err
	}
	currentUserText, err := contextconsensus.ExtractManagedCurrentUserText(protocol, body)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	managedRequest.Protocol = protocol
	managedRequest.IncrementalSourceDigest = contextconsensus.DigestManagedIncrementalRequest(body)
	managedRequest.CurrentUserText = currentUserText
	common.SetContextKey(c, constant.ContextKeyManagedContextRequest, managedRequest)
	stream, err := managedRequestStream(body)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	if stream {
		return nil, http.StatusBadRequest, fmt.Errorf("managed context does not support streaming requests")
	}
	settings := model_setting.GetSmartRoutingSettings()
	stateTTL := time.Duration(settings.ContextStateTTLSeconds) * time.Second
	if stateTTL <= 0 {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("managed context state TTL is unavailable")
	}
	if !common.RedisEnabled || common.RDB == nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("managed context Redis is unavailable")
	}
	runtime, err := contextconsensus.NewManagedConsensusRuntimeFromEnvironment(common.RDB, stateTTL)
	if err != nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("managed context runtime is unavailable")
	}
	leaseTTL := min(stateTTL, managedLeaseMaximumTTL)
	holderID := strings.TrimSpace(c.GetString(common.RequestIdKey))
	if holderID == "" {
		holderID = common.NewRequestId()
	}
	session, err := contextconsensus.BeginManagedConsensusSession(c.Request.Context(), runtime, contextconsensus.BeginManagedConsensusSessionRequest{
		Owner: contextconsensus.ManagedConsensusOwner{
			UserID:         common.GetContextKeyInt(c, constant.ContextKeyUserId),
			TokenID:        common.GetContextKeyInt(c, constant.ContextKeyTokenId),
			EndpointFamily: endpointFamily,
		},
		ExternalContextID: managedRequest.ExternalContextID,
		ExpectedRevision:  managedRequest.ExpectedRevision,
		HolderID:          holderID,
		LeaseTTL:          leaseTTL,
	})
	if err != nil {
		return nil, managedConsensusBeginStatus(err), managedConsensusBeginError(err)
	}
	preparedSuccessfully := false
	defer func() {
		if preparedSuccessfully {
			return
		}
		releaseContext, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 2*time.Second)
		defer cancel()
		_ = session.Close(releaseContext)
	}()
	state, err := session.State()
	if err != nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("managed context state is unavailable")
	}
	if state != nil && state.ProviderBinding != nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("managed context provider state is unavailable")
	}
	rewrittenBody, err := contextconsensus.PrepareManagedIncrementalRequest(protocol, body, state)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	newStorage, err := common.CreateBodyStorage(rewrittenBody)
	if err != nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("prepare managed request body storage: %w", err)
	}
	requestContext, lifecycle, err := contextconsensus.StartManagedConsensusLeaseGuard(c.Request.Context(), session, leaseTTL)
	if err != nil {
		_ = newStorage.Close()
		return nil, http.StatusServiceUnavailable, err
	}
	c.Request = c.Request.WithContext(requestContext)
	c.Set(common.KeyBodyStorage, newStorage)
	c.Set(common.KeyRequestBody, append([]byte(nil), rewrittenBody...))
	c.Request.Body = io.NopCloser(newStorage)
	c.Request.ContentLength = int64(len(rewrittenBody))
	c.Request.Header.Set("Content-Length", strconv.Itoa(len(rewrittenBody)))
	common.SetContextKey(c, constant.ContextKeyManagedContextSession, session)
	common.SetContextKey(c, constant.ContextKeyManagedContextLease, lifecycle)
	if storage != newStorage {
		_ = storage.Close()
	}
	preparedSuccessfully = true
	return lifecycle, 0, nil
}

func managedProtocolAndEndpointFamily(path string) (types.RelayFormat, string, error) {
	switch {
	case strings.HasPrefix(path, "/v1/chat/completions"), strings.HasPrefix(path, "/pg/chat/completions"):
		return types.RelayFormatOpenAI, "chat", nil
	case strings.HasPrefix(path, "/v1/responses") && !strings.HasPrefix(path, "/v1/responses/compact"):
		return types.RelayFormatOpenAIResponses, "responses", nil
	case strings.HasPrefix(path, "/v1/messages"):
		return types.RelayFormatClaude, "claude", nil
	case strings.HasPrefix(path, "/v1beta/models/"), strings.HasPrefix(path, "/v1/models/"):
		return types.RelayFormatGemini, "gemini", nil
	default:
		return "", "", fmt.Errorf("managed context is unsupported for this endpoint")
	}
}

func managedRequestStream(body []byte) (bool, error) {
	var request struct {
		Stream *bool `json:"stream"`
	}
	if err := common.Unmarshal(body, &request); err != nil {
		return false, fmt.Errorf("decode managed stream setting: %w", err)
	}
	return request.Stream != nil && *request.Stream, nil
}

func managedEndpointStreams(request *http.Request) bool {
	if request == nil || request.URL == nil {
		return false
	}
	path := strings.ToLower(request.URL.Path)
	if strings.Contains(path, ":streamgeneratecontent") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(request.URL.Query().Get("alt")), "sse")
}

func managedConsensusBeginStatus(err error) int {
	switch {
	case errors.Is(err, contextconsensus.ErrManagedConsensusRevisionConflict),
		errors.Is(err, contextconsensus.ErrManagedConsensusNotFound),
		errors.Is(err, contextconsensus.ErrManagedConsensusLeaseHeld):
		return http.StatusConflict
	default:
		return http.StatusServiceUnavailable
	}
}

func managedConsensusBeginError(err error) error {
	switch {
	case errors.Is(err, contextconsensus.ErrManagedConsensusRevisionConflict), errors.Is(err, contextconsensus.ErrManagedConsensusNotFound):
		return fmt.Errorf("managed context revision conflict")
	case errors.Is(err, contextconsensus.ErrManagedConsensusLeaseHeld):
		return fmt.Errorf("managed context request is already in progress")
	default:
		return fmt.Errorf("managed context state is unavailable")
	}
}
