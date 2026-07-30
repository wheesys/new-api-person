package middleware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
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
	contextIDHeader          = "X-New-Api-Context-Id"
	contextModeHeader        = "X-New-Api-Context-Mode"
	contextRevisionHeader    = "X-New-Api-Context-Revision"
	contextIdempotencyHeader = "X-New-Api-Context-Idempotency-Key"
	managedLeaseMaximumTTL   = 30 * time.Second
)

func captureContextConsensusPolicy(c *gin.Context) error {
	if c == nil || c.Request == nil {
		return nil
	}

	mode := strings.ToLower(strings.TrimSpace(c.Request.Header.Get(contextModeHeader)))
	contextID := strings.TrimSpace(c.Request.Header.Get(contextIDHeader))
	revisionValue := strings.TrimSpace(c.Request.Header.Get(contextRevisionHeader))
	idempotencyValues := c.Request.Header.Values(contextIdempotencyHeader)
	idempotencyKey := ""
	if len(idempotencyValues) == 1 {
		idempotencyKey = idempotencyValues[0]
	}

	c.Request.Header.Del(contextIDHeader)
	c.Request.Header.Del(contextModeHeader)
	c.Request.Header.Del(contextRevisionHeader)
	c.Request.Header.Del(contextIdempotencyHeader)

	if mode != "" && mode != "full" && mode != "auto_compact" {
		if mode != "managed" {
			return fmt.Errorf("unsupported context mode")
		}
	}

	settings := model_setting.GetSmartRoutingSettings()
	if mode == "managed" {
		if contextID == "" || revisionValue == "" || len(idempotencyValues) != 1 || !validManagedContextIdempotencyKey(idempotencyKey) {
			return fmt.Errorf("managed context requires a valid context ID, revision, and idempotency key")
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
			IdempotencyKey:    idempotencyKey,
			ExpectedRevision:  expectedRevision,
		})
		common.SetContextKey(c, constant.ContextKeySuppressDebugLog, true)
	} else if contextID != "" || revisionValue != "" || len(idempotencyValues) != 0 {
		return fmt.Errorf("context ID, revision, and idempotency key require managed context mode")
	}
	policy := contextconsensus.CompactionPolicy{
		SystemEnabled:             settings.ContextConsensusEnabled && settings.AutoCompactionEnabled,
		AllowToolResultCompaction: settings.AllowToolResultCompaction,
		PolicyVersion:             "context-consensus-v1",
		PreservedRecentTurns:      settings.PreservedRecentTurns,
		TargetInputTokens:         settings.MaxCompactionInputTokens,
		MaxSummaryTokens:          settings.MaxSummaryTokens,
	}
	snapshot := policy.Snapshot(
		common.GetContextKeyBool(c, constant.ContextKeyTokenContextCompaction),
		mode == "auto_compact" || mode == "managed",
	)
	common.SetContextKey(c, constant.ContextKeyContextConsensusPolicy, snapshot)
	return nil
}

func validManagedContextIdempotencyKey(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || character == '.' || character == '_' || character == '~' || character == '-' {
			continue
		}
		return false
	}
	return true
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
	if managedRequest.ExpectedRevision >= math.MaxInt64 {
		return nil, http.StatusConflict, fmt.Errorf("managed context revision cannot advance")
	}
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
	runtime, err := contextconsensus.NewManagedConsensusCryptoRuntimeFromEnvironment()
	if err != nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("managed context runtime is unavailable")
	}
	policy, policyFound := common.GetContextKeyType[contextconsensus.CompactionPolicySnapshot](c, constant.ContextKeyContextConsensusPolicy)
	if !policyFound {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("managed context policy is unavailable")
	}
	owner := contextconsensus.ManagedConsensusOwner{
		UserID: common.GetContextKeyInt(c, constant.ContextKeyUserId), TokenID: common.GetContextKeyInt(c, constant.ContextKeyTokenId), EndpointFamily: endpointFamily,
	}
	managedRequest.Owner = owner
	common.SetContextKey(c, constant.ContextKeyManagedContextRequest, managedRequest)
	outcome, err := contextconsensus.ReserveManagedOutcome(c.Request.Context(), runtime, contextconsensus.ManagedOutcomeRequest{
		Owner: owner, ExternalContextID: managedRequest.ExternalContextID, IdempotencyKey: managedRequest.IdempotencyKey,
		ExpectedRevision: managedRequest.ExpectedRevision, Protocol: protocol,
		IncrementalSourceDigest: managedRequest.IncrementalSourceDigest, PolicyVersion: policy.PolicyVersion, TTL: stateTTL,
	})
	if err != nil {
		if contextconsensus.IsManagedOutcomeConflict(err) {
			return nil, http.StatusConflict, fmt.Errorf("managed context idempotency intent conflicts with an existing request")
		}
		return nil, http.StatusServiceUnavailable, fmt.Errorf("managed context outcome is unavailable")
	}
	common.SetContextKey(c, constant.ContextKeyManagedContextOutcome, outcome)
	managedRequest.IdempotencyKey = ""
	common.SetContextKey(c, constant.ContextKeyManagedContextRequest, managedRequest)
	if outcome.Phase() == contextconsensus.ManagedOutcomePhaseCommitted {
		replay, replayErr := outcome.Response(c.Request.Context())
		if replayErr != nil {
			return nil, http.StatusServiceUnavailable, fmt.Errorf("managed context committed outcome is unavailable")
		}
		common.SetContextKey(c, constant.ContextKeyManagedContextReplay, replay)
		return nil, 0, nil
	}
	if outcome.Phase() == contextconsensus.ManagedOutcomePhaseMainDispatched || outcome.Phase() == contextconsensus.ManagedOutcomePhaseSummaryDispatched {
		return nil, http.StatusServiceUnavailable, contextconsensus.ErrManagedOutcomeUnknown
	}
	if outcome.Phase() == contextconsensus.ManagedOutcomePhaseTerminalFailed || outcome.Phase() == contextconsensus.ManagedOutcomePhaseExpired {
		return nil, http.StatusConflict, fmt.Errorf("managed context outcome is no longer reusable")
	}
	if !common.RedisEnabled || common.RDB == nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("managed context Redis is unavailable")
	}
	repository, err := contextconsensus.NewRedisManagedConsensusRepository(common.RDB, stateTTL)
	if err != nil || runtime.AttachRepository(repository) != nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("managed context Redis repository is unavailable")
	}
	if outcome.Phase() == contextconsensus.ManagedOutcomePhaseSettledPendingCommit {
		nextState, nextStateErr := outcome.NextState(c.Request.Context())
		if nextStateErr != nil {
			return nil, http.StatusServiceUnavailable, fmt.Errorf("managed context pending outcome is unavailable")
		}
		snapshot, snapshotErr := outcome.SummaryExecution(c.Request.Context())
		if snapshotErr != nil {
			return nil, http.StatusServiceUnavailable, fmt.Errorf("managed context pending summary snapshot is unavailable")
		}
		committed, currentAtExpected, confirmErr := runtime.ConfirmManagedOutcomeCommit(
			c.Request.Context(), owner, managedRequest.ExternalContextID, managedRequest.ExpectedRevision, nextState, snapshot.Plan.ProviderStateCommit,
		)
		if confirmErr != nil {
			return nil, http.StatusServiceUnavailable, fmt.Errorf("managed context commit outcome is unavailable")
		}
		if committed {
			if err := outcome.MarkCommitted(c.Request.Context()); err != nil {
				return nil, http.StatusServiceUnavailable, fmt.Errorf("managed context committed outcome is unavailable")
			}
			replay, replayErr := outcome.Response(c.Request.Context())
			if replayErr != nil {
				return nil, http.StatusServiceUnavailable, fmt.Errorf("managed context committed response is unavailable")
			}
			common.SetContextKey(c, constant.ContextKeyManagedContextReplay, replay)
			return nil, 0, nil
		}
		if !currentAtExpected {
			return nil, http.StatusServiceUnavailable, contextconsensus.ErrManagedOutcomeUnknown
		}
	}
	leaseTTL := min(stateTTL, managedLeaseMaximumTTL)
	holderID := strings.TrimSpace(c.GetString(common.RequestIdKey))
	if holderID == "" {
		holderID = common.NewRequestId()
	}
	session, err := contextconsensus.BeginManagedConsensusSession(c.Request.Context(), runtime, contextconsensus.BeginManagedConsensusSessionRequest{
		Owner:             owner,
		ExternalContextID: managedRequest.ExternalContextID,
		ExpectedRevision:  managedRequest.ExpectedRevision,
		HolderID:          holderID,
		LeaseTTL:          leaseTTL,
	})
	if err != nil {
		return nil, managedConsensusBeginStatus(err), managedConsensusBeginError(err)
	}
	billingLookupCandidates, billingExpectedRevision, err := session.BillingOperationLookupCandidates()
	if err != nil || billingExpectedRevision != managedRequest.ExpectedRevision {
		_ = session.Close(c.Request.Context())
		return nil, http.StatusServiceUnavailable, fmt.Errorf("managed context billing identity is unavailable")
	}
	managedRequest.BillingLookupCandidates = billingLookupCandidates
	common.SetContextKey(c, constant.ContextKeyManagedContextRequest, managedRequest)
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
	providerStateReference, err := contextconsensus.ExtractManagedProviderStateReference(protocol, body)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	if state != nil && state.ProviderBinding != nil {
		if providerStateReference == "" {
			return nil, http.StatusBadRequest, fmt.Errorf("managed request is missing the bound provider state reference")
		}
		resolution, resolveErr := session.ResolveBoundProviderState(c.Request.Context(), owner, providerStateReference, *state)
		if resolveErr != nil {
			if errors.Is(resolveErr, contextconsensus.ErrProviderStateBindingNotFound) || errors.Is(resolveErr, contextconsensus.ErrProviderStateBindingConflict) || errors.Is(resolveErr, contextconsensus.ErrManagedConsensusKeyConflict) {
				return nil, http.StatusConflict, fmt.Errorf("managed context provider state binding is invalid")
			}
			return nil, http.StatusServiceUnavailable, fmt.Errorf("managed context provider state is unavailable")
		}
		common.SetContextKey(c, constant.ContextKeyManagedProviderState, resolution)
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
