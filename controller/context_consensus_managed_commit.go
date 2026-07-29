package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
)

const managedMainResponseMaximumBytes = int(defaultInternalCompactionResponseBytes)

type managedRevisionCommitter interface {
	CommitWithRecovery(context.Context, contextconsensus.ManagedConsensusState, time.Duration) (contextconsensus.ManagedConsensusCommitResult, error)
}

func executeManagedContextAttempt(
	c *gin.Context,
	info *relaycommon.RelayInfo,
	attempt *relay.PreparedTextRelayAttempt,
	managedRequest contextconsensus.ManagedContextRequest,
	finishMainAttempt func(),
) (bool, *types.NewAPIError) {
	session, ok := common.GetContextKeyType[*contextconsensus.ManagedConsensusSession](c, constant.ContextKeyManagedContextSession)
	if !ok || session == nil {
		return false, managedExecutionError(fmt.Errorf("managed consensus session is unavailable"))
	}
	guard, ok := common.GetContextKeyType[*contextconsensus.ManagedConsensusLeaseGuard](c, constant.ContextKeyManagedContextLease)
	if !ok || guard == nil {
		return false, managedExecutionError(fmt.Errorf("managed consensus lease guard is unavailable"))
	}
	if err := errors.Join(c.Request.Context().Err(), guard.RenewalError()); err != nil {
		return false, managedExecutionError(fmt.Errorf("managed consensus lease is unhealthy: %w", err))
	}
	previousState, err := session.State()
	if err != nil {
		return false, managedExecutionError(err)
	}
	policy, ok := common.GetContextKeyType[contextconsensus.CompactionPolicySnapshot](c, constant.ContextKeyContextConsensusPolicy)
	if !ok || strings.TrimSpace(policy.PolicyVersion) == "" {
		return false, managedExecutionError(fmt.Errorf("managed consensus policy snapshot is unavailable"))
	}
	settings := model_setting.GetSmartRoutingSettings()
	if len(settings.CompactionModelPool) == 0 || len(settings.CompactionChannelIDs) == 0 ||
		settings.MaxSummaryTokens <= 0 || settings.MaxCompactionInputTokens <= 0 ||
		settings.MaxCompactionQuota <= 0 || settings.CompactionTimeoutSeconds <= 0 ||
		settings.MaxCompactionCallsPerRequest != 1 {
		return false, managedExecutionError(fmt.Errorf("managed consensus summary authorization is incomplete"))
	}
	stateTTL := time.Duration(settings.ContextStateTTLSeconds) * time.Second
	if stateTTL <= 0 {
		return false, managedExecutionError(fmt.Errorf("managed consensus state TTL is unavailable"))
	}
	compactionModel := strings.TrimSpace(settings.CompactionModelPool[0])
	if err := contextconsensus.ValidateExplicitCompactionModel(compactionModel); err != nil {
		return false, managedExecutionError(err)
	}

	mainResult, newAPIError := executeManagedPreparedTextAttempt(
		c,
		info,
		attempt,
		managedRequest.Protocol,
		managedMainResponseMaximumBytes,
		managedMainExecutionDependencies{
			execute: func(contextValue *gin.Context, relayInfo *relaycommon.RelayInfo, preparedAttempt *relay.PreparedTextRelayAttempt) (*dto.Usage, *types.NewAPIError) {
				if preparedAttempt != nil {
					return relay.ExecutePreparedTextRelayAttempt(contextValue, relayInfo, preparedAttempt)
				}
				return relay.ExecuteTextAttemptWithoutQuota(contextValue, relayInfo)
			},
			settle: relay.SettleTextRelayUsage,
		},
	)
	if finishMainAttempt != nil {
		finishMainAttempt()
	}
	if newAPIError != nil {
		return true, newAPIError
	}
	evidence := contextconsensus.ManagedRevisionEvidence{
		Protocol:                managedRequest.Protocol,
		PreviousState:           previousState,
		IncrementalSourceDigest: managedRequest.IncrementalSourceDigest,
		CurrentUserText:         managedRequest.CurrentUserText,
		AssistantOutput:         mainResult.Output,
		PolicyVersion:           policy.PolicyVersion,
	}
	plan, err := contextconsensus.BuildManagedRevisionPlan(evidence)
	if err != nil {
		return true, managedExecutionError(err)
	}
	summaryRequest, err := contextconsensus.BuildManagedRevisionPrompt(compactionModel, evidence, plan, settings.MaxSummaryTokens)
	if err != nil {
		return true, managedExecutionError(err)
	}
	executor, err := NewInternalCompactionExecutor(InternalCompactionExecutorRequest{
		ParentContext:     c,
		Model:             compactionModel,
		ModelPool:         settings.CompactionModelPool,
		AllowedChannelIDs: settings.CompactionChannelIDs,
		PolicyVersion:     policy.PolicyVersion,
		SourceDigest:      plan.SourceDigest,
		MaxOutputTokens:   settings.MaxSummaryTokens,
		MaxInputTokens:    settings.MaxCompactionInputTokens,
		SummaryRequest:    summaryRequest,
		Plan: contextconsensus.CompactionPlan{
			Protocol:         managedRequest.Protocol,
			SourceDigest:     plan.SourceDigest,
			MaxSummaryTokens: settings.MaxSummaryTokens,
			PolicyVersion:    policy.PolicyVersion,
		},
		ManagedRevisionPlan: &plan,
		MaxQuota:            settings.MaxCompactionQuota,
		Timeout:             time.Duration(settings.CompactionTimeoutSeconds) * time.Second,
		MaxResponseBytes:    defaultInternalCompactionResponseBytes,
	})
	if err != nil {
		return true, managedExecutionError(err)
	}
	childResult, err := executor.Execute(c.Request.Context())
	if err != nil || !childResult.Succeeded || childResult.Summary == nil {
		if err == nil {
			err = fmt.Errorf("managed consensus summary child returned no committed summary")
		}
		return true, managedExecutionError(fmt.Errorf("managed consensus summary child failed: %w", err))
	}
	if err := errors.Join(c.Request.Context().Err(), guard.RenewalError()); err != nil {
		return true, managedExecutionError(fmt.Errorf("managed consensus lease is unhealthy: %w", err))
	}
	return true, commitAndFlushManagedRevision(c, session, plan, *childResult.Summary, mainResult.buffer, stateTTL, time.Now())
}

func commitAndFlushManagedRevision(
	c *gin.Context,
	committer managedRevisionCommitter,
	plan contextconsensus.ManagedRevisionPlan,
	summary contextconsensus.ConsensusSummary,
	buffer *managedResponseBuffer,
	stateTTL time.Duration,
	now time.Time,
) *types.NewAPIError {
	if c == nil || c.Request == nil || committer == nil || buffer == nil {
		return managedExecutionError(fmt.Errorf("managed consensus commit barrier is incomplete"))
	}
	nextState, err := contextconsensus.BuildNextManagedConsensusState(plan, summary, now)
	if err != nil {
		return managedExecutionError(err)
	}
	if _, err := committer.CommitWithRecovery(c.Request.Context(), nextState, stateTTL); err != nil {
		return managedCommitAPIError(err)
	}
	buffer.Header().Set("X-New-Api-Context-Revision", fmt.Sprintf("%d", nextState.Revision))
	if err := buffer.FlushToClient(); err != nil {
		// The revision is durable at this point. A client write failure must not
		// trigger a refund, replay, or a second gateway error body.
		return nil
	}
	return nil
}

func managedCommitAPIError(err error) *types.NewAPIError {
	errorCode := types.ErrorCodeManagedContextCommitFailed
	if errors.Is(err, contextconsensus.ErrManagedConsensusOutcomeUnknown) {
		errorCode = types.ErrorCodeManagedContextCommitOutcomeUnknown
	}
	return types.NewErrorWithStatusCode(err, errorCode, http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry())
}
