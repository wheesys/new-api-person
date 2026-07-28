package controller

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/service/smartrouting"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

type contextConsensusMainCommit struct {
	request              dto.Request
	relayInfo            *relaycommon.RelayInfo
	attempt              *relay.PreparedTextRelayAttempt
	budget               contextconsensus.TokenBudget
	candidate            smartrouting.SmartRouteCandidate
	channel              *model.Channel
	context              *gin.Context
	bodyStorage          common.BodyStorage
	compacted            bool
	inputBefore          int
	inputAfter           int
	summaryDigest        string
	compactionModel      string
	compactionChannelID  int
	compactionRequestID  string
	compactionQuota      int
	compactionResultCode string
	committed            bool
}

type contextConsensusCandidatePreparer func(
	parent *gin.Context,
	relayFormat types.RelayFormat,
	sourceBody []byte,
	candidate smartrouting.SmartRouteCandidate,
	counter contextconsensus.TokenCounter,
	resolver contextconsensus.ContextLimitResolver,
	safetyMargin int,
) (*contextConsensusMainCommit, error)

func (commit *contextConsensusMainCommit) Close() {
	if commit == nil || commit.committed {
		return
	}
	if commit.attempt != nil {
		_ = commit.attempt.Close()
	}
	if commit.bodyStorage != nil {
		_ = commit.bodyStorage.Close()
	}
}

func prepareContextConsensusMainRequest(c *gin.Context, relayFormat types.RelayFormat) (*contextConsensusMainCommit, error) {
	policy, ok := common.GetContextKeyType[contextconsensus.CompactionPolicySnapshot](c, constant.ContextKeyContextConsensusPolicy)
	if !ok || !policy.SystemEnabled {
		return nil, nil
	}
	if relayFormat != types.RelayFormatOpenAI && relayFormat != types.RelayFormatOpenAIResponses && relayFormat != types.RelayFormatClaude && relayFormat != types.RelayFormatGemini {
		return nil, fmt.Errorf("ContextConsensus does not support relay format %q", relayFormat)
	}
	candidates, ok := common.GetContextKeyType[[]smartrouting.SmartRouteCandidate](c, constant.ContextKeySmartRoutingFrozenCandidates)
	if !ok || len(candidates) == 0 {
		return nil, fmt.Errorf("ContextConsensus authoritative candidate set is unavailable")
	}
	candidates = append([]smartrouting.SmartRouteCandidate(nil), candidates...)
	settings := model_setting.GetSmartRoutingSettings()
	resolver := contextconsensus.NewFrozenContextLimitResolver(settings)
	counter := contextconsensus.NewProductionTokenCounter()

	sourceBody, err := currentRequestBody(c)
	if err != nil {
		return nil, err
	}
	commit, allOverLimit, inputBefore, err := evaluateContextConsensusCandidates(c, relayFormat, sourceBody, candidates, counter, resolver, settings.ContextSafetyMarginTokens, prepareContextConsensusCandidate)
	if err != nil {
		return nil, err
	}
	if commit != nil {
		commit.inputBefore = inputBefore
		commit.inputAfter = commit.budget.PromptTokens
		return commit, nil
	}
	if !allOverLimit {
		return nil, fmt.Errorf("ContextConsensus could not establish authoritative full-context evidence")
	}
	authorization := contextconsensus.EvaluateCompactionAuthorization(policy)
	if !authorization.Allowed {
		return nil, fmt.Errorf("full context exceeds every authoritative candidate and compaction is not authorized: %s", strings.Join(authorization.ReasonCodes, ","))
	}
	if settings.MaxCompactionCallsPerRequest != 1 {
		return nil, fmt.Errorf("ContextConsensus requires exactly one allowed compaction call")
	}
	if len(settings.CompactionModelPool) == 0 || len(settings.CompactionChannelIDs) == 0 || settings.MaxCompactionQuota <= 0 || settings.CompactionTimeoutSeconds <= 0 || settings.MaxCompactionInputTokens <= 0 {
		return nil, fmt.Errorf("ContextConsensus compaction limits and authorization sets are incomplete")
	}

	envelope, err := contextconsensus.Extract(contextconsensus.ExtractionRequest{Protocol: relayFormat, Body: sourceBody})
	if err != nil {
		return nil, fmt.Errorf("extract compaction source: %w", err)
	}
	plan, err := contextconsensus.BuildCompactionPlan(contextconsensus.CompactionPlanRequest{
		Protocol: relayFormat,
		Body:     sourceBody,
		Envelope: envelope,
		Policy:   policy,
	})
	if err != nil {
		return nil, err
	}
	compactionModel := strings.TrimSpace(settings.CompactionModelPool[0])
	summaryRequest, err := contextconsensus.BuildCompactionPrompt(contextconsensus.CompactionPromptRequest{
		Model:    compactionModel,
		Protocol: relayFormat,
		Body:     sourceBody,
		Plan:     plan,
	})
	if err != nil {
		return nil, err
	}
	summaryBody, err := common.Marshal(summaryRequest)
	if err != nil {
		return nil, fmt.Errorf("encode compaction request: %w", err)
	}
	summaryCount, err := counter.CountPromptTokens(c.Request.Context(), contextconsensus.TokenCountRequest{
		Protocol:    types.RelayFormatOpenAI,
		Model:       compactionModel,
		ChannelID:   settings.CompactionChannelIDs[0],
		RequestBody: summaryBody,
	})
	if err != nil {
		return nil, fmt.Errorf("count compaction input: %w", err)
	}
	if summaryCount.Breakdown.PromptTokens() > settings.MaxCompactionInputTokens {
		return nil, fmt.Errorf("compaction input exceeds configured maximum")
	}
	executor, err := NewInternalCompactionExecutor(InternalCompactionExecutorRequest{
		ParentContext:     c,
		Model:             compactionModel,
		ModelPool:         settings.CompactionModelPool,
		AllowedChannelIDs: settings.CompactionChannelIDs,
		PolicyVersion:     policy.PolicyVersion,
		SourceDigest:      plan.SourceDigest,
		MaxOutputTokens:   plan.MaxSummaryTokens,
		MaxInputTokens:    settings.MaxCompactionInputTokens,
		SummaryRequest:    summaryRequest,
		Plan:              plan,
		MaxQuota:          settings.MaxCompactionQuota,
		Timeout:           time.Duration(settings.CompactionTimeoutSeconds) * time.Second,
	})
	if err != nil {
		return nil, err
	}
	childResult, err := executor.Execute(c.Request.Context())
	if err != nil {
		return nil, err
	}
	if !childResult.Succeeded || childResult.Summary == nil {
		return nil, fmt.Errorf("compaction child did not return a committed summary")
	}
	validatedSummary, err := common.Marshal(childResult.Summary)
	if err != nil {
		return nil, fmt.Errorf("encode validated compaction summary: %w", err)
	}
	rewrittenBody, err := contextconsensus.RewriteRequestWithConsensus(contextconsensus.RewriteCompactedRequest{
		Protocol:    relayFormat,
		Body:        sourceBody,
		Plan:        plan,
		SummaryBody: validatedSummary,
	})
	if err != nil {
		return nil, err
	}
	commit, _, _, err = evaluateContextConsensusCandidates(c, relayFormat, rewrittenBody, candidates, counter, resolver, settings.ContextSafetyMarginTokens, prepareContextConsensusCandidate)
	if err != nil {
		return nil, err
	}
	if commit == nil {
		return nil, fmt.Errorf("compacted context still exceeds every authoritative candidate")
	}
	commit.compacted = true
	commit.inputBefore = inputBefore
	commit.inputAfter = commit.budget.PromptTokens
	commit.summaryDigest = childResult.SummaryDigest
	commit.compactionModel = childResult.Model
	commit.compactionChannelID = executor.SelectedChannelID()
	commit.compactionRequestID = childResult.ChildRequestID
	commit.compactionQuota = childResult.SettledQuota
	commit.compactionResultCode = childResult.ResultCode
	return commit, nil
}

func evaluateContextConsensusCandidates(
	parent *gin.Context,
	relayFormat types.RelayFormat,
	sourceBody []byte,
	candidates []smartrouting.SmartRouteCandidate,
	counter contextconsensus.TokenCounter,
	resolver contextconsensus.ContextLimitResolver,
	safetyMargin int,
	prepareCandidate contextConsensusCandidatePreparer,
) (*contextConsensusMainCommit, bool, int, error) {
	if prepareCandidate == nil {
		return nil, false, 0, fmt.Errorf("ContextConsensus candidate preparer is unavailable")
	}
	allOverLimit := true
	inputTokens := 0
	errorsByCandidate := make([]string, 0)
	for _, candidate := range candidates {
		commit, err := prepareCandidate(parent, relayFormat, sourceBody, candidate, counter, resolver, safetyMargin)
		if err != nil {
			allOverLimit = false
			errorsByCandidate = append(errorsByCandidate, fmt.Sprintf("model=%s channel=%d: %v", candidate.ModelName, candidate.ChannelID, err))
			continue
		}
		if commit.budget.PromptTokens > inputTokens {
			inputTokens = commit.budget.PromptTokens
		}
		if commit.budget.Fits {
			return commit, false, inputTokens, nil
		}
		commit.Close()
	}
	if len(errorsByCandidate) > 0 {
		return nil, false, inputTokens, fmt.Errorf("ContextConsensus authoritative candidate validation failed: %s", strings.Join(errorsByCandidate, "; "))
	}
	return nil, allOverLimit, inputTokens, nil
}

func prepareContextConsensusCandidate(
	parent *gin.Context,
	relayFormat types.RelayFormat,
	sourceBody []byte,
	candidate smartrouting.SmartRouteCandidate,
	counter contextconsensus.TokenCounter,
	resolver contextconsensus.ContextLimitResolver,
	safetyMargin int,
) (*contextConsensusMainCommit, error) {
	channel, err := model.GetChannelById(candidate.ChannelID, true)
	if err != nil || channel == nil {
		return nil, fmt.Errorf("load candidate channel: %w", err)
	}
	if channel.Status != common.ChannelStatusEnabled {
		return nil, fmt.Errorf("candidate channel is unavailable")
	}
	body, err := rewriteContextConsensusSourceModel(relayFormat, sourceBody, candidate.ModelName)
	if err != nil {
		return nil, err
	}
	candidateContext, storage, err := newContextConsensusCandidateContext(parent, body, relayFormat, candidate)
	if err != nil {
		return nil, err
	}
	commit := &contextConsensusMainCommit{candidate: candidate, channel: channel, context: candidateContext, bodyStorage: storage}
	preparedSuccessfully := false
	defer func() {
		if !preparedSuccessfully {
			commit.Close()
		}
	}()
	clearContextConsensusChannelKeys(candidateContext)
	channelKey, keyIndex, err := frozenContextConsensusChannelKey(channel)
	if err != nil {
		return nil, err
	}
	if apiError := middleware.SetupContextForSelectedChannelSnapshot(candidateContext, channel, candidate.ModelName, channelKey, keyIndex); apiError != nil {
		return nil, apiError
	}
	if candidate.Group != "" {
		common.SetContextKey(candidateContext, constant.ContextKeyUsingGroup, candidate.Group)
	}
	request, err := helper.GetAndValidateRequest(candidateContext, relayFormat)
	if err != nil {
		return nil, fmt.Errorf("parse candidate request: %w", err)
	}
	relayInfo, err := relaycommon.GenRelayInfo(candidateContext, relayFormat, request, nil)
	if err != nil {
		return nil, fmt.Errorf("build candidate relay info: %w", err)
	}
	attempt, apiError := relay.PrepareAuthoritativeTextRelayAttempt(candidateContext, relayInfo)
	if apiError != nil {
		return nil, apiError
	}
	commit.attempt = attempt
	prepared := attempt.PreparedRequest()
	if prepared == nil {
		return nil, fmt.Errorf("authoritative prepared request is unavailable")
	}
	requestedMaxOutput := prepared.RequestedMaxOutput()
	if requestedMaxOutput == nil {
		return nil, fmt.Errorf("authoritative default max output is unavailable")
	}
	preparedBody, err := prepared.Body()
	if err != nil {
		return nil, err
	}
	budget, err := contextconsensus.EvaluateResolvedTokenBudget(candidateContext.Request.Context(), counter, resolver, contextconsensus.TokenCountRequest{
		Protocol:    prepared.Protocol(),
		Model:       prepared.Model(),
		ChannelID:   candidate.ChannelID,
		RequestBody: preparedBody,
	}, contextconsensus.ResolvedTokenBudgetRequest{
		RequestedMaxOutput: requestedMaxOutput,
		SafetyMarginTokens: safetyMargin,
	})
	if err != nil {
		return nil, err
	}
	commit.request = request
	commit.relayInfo = relayInfo
	commit.budget = budget
	preparedSuccessfully = true
	return commit, nil
}

func frozenContextConsensusChannelKey(channel *model.Channel) (string, int, error) {
	if channel == nil {
		return "", 0, fmt.Errorf("candidate channel is required")
	}
	if !channel.ChannelInfo.IsMultiKey {
		if strings.TrimSpace(channel.Key) == "" {
			return "", 0, fmt.Errorf("candidate channel key is unavailable")
		}
		return channel.Key, 0, nil
	}
	keys := channel.GetKeys()
	for index, key := range keys {
		status := common.ChannelStatusEnabled
		if configuredStatus, ok := channel.ChannelInfo.MultiKeyStatusList[index]; ok {
			status = configuredStatus
		}
		if status == common.ChannelStatusEnabled && strings.TrimSpace(key) != "" {
			return key, index, nil
		}
	}
	return "", 0, fmt.Errorf("candidate channel has no enabled credential slot")
}

func commitContextConsensusMainRequest(c *gin.Context, commit *contextConsensusMainCommit) error {
	if c == nil || commit == nil || commit.context == nil || commit.context.Request == nil || commit.bodyStorage == nil || commit.attempt == nil {
		return fmt.Errorf("ContextConsensus main commit is incomplete")
	}
	oldStorage, err := common.GetBodyStorage(c)
	if err != nil {
		return err
	}
	body, err := commit.bodyStorage.Bytes()
	if err != nil {
		return err
	}
	if _, err := commit.bodyStorage.Seek(0, io.SeekStart); err != nil {
		return err
	}
	for key, value := range commit.context.Keys {
		c.Set(key, value)
	}
	c.Request = commit.context.Request
	c.Set(common.KeyBodyStorage, commit.bodyStorage)
	c.Set(common.KeyRequestBody, append([]byte(nil), body...))
	c.Request.Body = io.NopCloser(commit.bodyStorage)
	c.Request.ContentLength = int64(len(body))
	c.Request.Header.Set("Content-Length", strconv.Itoa(len(body)))
	if oldStorage != commit.bodyStorage {
		_ = oldStorage.Close()
	}
	common.SetContextKey(c, constant.ContextKeySmartRoutingRetryCandidates, []smartrouting.SmartRouteCandidate{commit.candidate})
	if decision, ok := common.GetContextKeyType[smartrouting.Decision](c, constant.ContextKeySmartRoutingDecision); ok {
		decision.SelectedModel = commit.candidate.ModelName
		decision.SelectedChannelID = commit.candidate.ChannelID
		decision.SelectedHealth = commit.candidate.HealthState
		decision.ScoreFactors = commit.candidate.ScoreFactors
		decision.ContextConsensus.Mode = "stateless_full_context"
		decision.ContextConsensus.Compacted = commit.compacted
		decision.ContextConsensus.InputTokensBefore = commit.inputBefore
		decision.ContextConsensus.InputTokensAfter = commit.inputAfter
		if commit.compacted {
			decision.ContextConsensus.Mode = "stateless_compacted"
			decision.ContextConsensus.SummaryDigest = commit.summaryDigest
			decision.ContextConsensus.CompactionModel = commit.compactionModel
			decision.ContextConsensus.CompactionChannelID = commit.compactionChannelID
			decision.ContextConsensus.CompactionRequestID = commit.compactionRequestID
			decision.ContextConsensus.CompactionQuota = commit.compactionQuota
			decision.ContextConsensus.CompactionResultCode = commit.compactionResultCode
		}
		common.SetContextKey(c, constant.ContextKeySmartRoutingDecision, decision)
	}
	commit.committed = true
	return nil
}

func currentRequestBody(c *gin.Context) ([]byte, error) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	body, err := storage.Bytes()
	if err != nil {
		return nil, err
	}
	if _, err := storage.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	c.Request.Body = io.NopCloser(storage)
	return append([]byte(nil), body...), nil
}

func rewriteContextConsensusSourceModel(relayFormat types.RelayFormat, body []byte, modelName string) ([]byte, error) {
	var request dto.Request
	switch relayFormat {
	case types.RelayFormatOpenAI:
		request = &dto.GeneralOpenAIRequest{}
	case types.RelayFormatOpenAIResponses:
		request = &dto.OpenAIResponsesRequest{}
	case types.RelayFormatClaude:
		request = &dto.ClaudeRequest{}
	case types.RelayFormatGemini:
		request = &dto.GeminiChatRequest{}
	default:
		return nil, fmt.Errorf("unsupported ContextConsensus source protocol %q", relayFormat)
	}
	if err := common.Unmarshal(body, request); err != nil {
		return nil, err
	}
	request.SetModelName(modelName)
	return common.Marshal(request)
}

func newContextConsensusCandidateContext(parent *gin.Context, body []byte, relayFormat types.RelayFormat, candidate smartrouting.SmartRouteCandidate) (*gin.Context, common.BodyStorage, error) {
	storage, err := common.CreateBodyStorage(body)
	if err != nil {
		return nil, nil, err
	}
	candidateContext := parent.Copy()
	candidateContext.Request = parent.Request.Clone(parent.Request.Context())
	if relayFormat == types.RelayFormatGemini {
		candidateContext.Request.URL.Path = rewriteGeminiModelPath(candidateContext.Request.URL.Path, candidate.ModelName)
	}
	candidateContext.Request.Body = io.NopCloser(storage)
	candidateContext.Request.ContentLength = int64(len(body))
	candidateContext.Request.Header.Set("Content-Length", strconv.Itoa(len(body)))
	candidateContext.Set(common.KeyBodyStorage, storage)
	candidateContext.Set(common.KeyRequestBody, append([]byte(nil), body...))
	return candidateContext, storage, nil
}

func rewriteGeminiModelPath(path, modelName string) string {
	modelsIndex := strings.Index(path, "/models/")
	if modelsIndex < 0 {
		return path
	}
	modelStart := modelsIndex + len("/models/")
	colonIndex := strings.Index(path[modelStart:], ":")
	if colonIndex < 0 {
		return path[:modelStart] + modelName
	}
	return path[:modelStart] + modelName + path[modelStart+colonIndex:]
}

func clearContextConsensusChannelKeys(c *gin.Context) {
	keys := []string{
		string(constant.ContextKeyChannelId), string(constant.ContextKeyChannelName), string(constant.ContextKeyChannelType),
		string(constant.ContextKeyChannelCreateTime), string(constant.ContextKeyChannelSetting), string(constant.ContextKeyChannelOtherSetting),
		string(constant.ContextKeyChannelPriceRatio), string(constant.ContextKeyChannelParamOverride), string(constant.ContextKeyChannelHeaderOverride),
		string(constant.ContextKeyChannelOrganization), string(constant.ContextKeyChannelAutoBan), string(constant.ContextKeyChannelModelMapping),
		string(constant.ContextKeyChannelStatusCodeMapping), string(constant.ContextKeyChannelIsMultiKey), string(constant.ContextKeyChannelMultiKeyIndex),
		string(constant.ContextKeyChannelKey), string(constant.ContextKeyChannelBaseUrl), "api_version", "region", "plugin",
	}
	for _, key := range keys {
		delete(c.Keys, key)
	}
}

func contextConsensusAPIError(err error) *types.NewAPIError {
	return types.NewErrorWithStatusCode(err, types.ErrorCodeCountTokenFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}
