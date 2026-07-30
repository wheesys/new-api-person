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
	"github.com/QuantumNous/new-api/model"
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

type managedProviderStateRevisionCommitter interface {
	CommitWithProviderStateRecovery(context.Context, contextconsensus.ManagedConsensusState, contextconsensus.ManagedProviderStateCommit, time.Duration) (contextconsensus.ManagedConsensusCommitResult, error)
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

	outcome, outcomeFound := common.GetContextKeyType[*contextconsensus.ManagedOutcomeSession](c, constant.ContextKeyManagedContextOutcome)
	if !outcomeFound || outcome == nil || outcome.Phase() != contextconsensus.ManagedOutcomePhaseIntent {
		return false, managedExecutionError(fmt.Errorf("managed context outcome intent is unavailable"))
	}
	var evidence contextconsensus.ManagedRevisionEvidence
	var plan contextconsensus.ManagedRevisionPlan
	var summaryRequest *dto.GeneralOpenAIRequest
	var summarySnapshot contextconsensus.ManagedSummaryExecutionSnapshot
	mainResult, newAPIError := executeManagedPreparedTextAttempt(
		c,
		info,
		attempt,
		managedRequest.Protocol,
		managedMainResponseMaximumBytes,
		managedMainExecutionDependencies{
			beforeDispatch: func(contextValue *gin.Context) error {
				return outcome.MarkMainDispatched(contextValue.Request.Context())
			},
			execute: func(contextValue *gin.Context, relayInfo *relaycommon.RelayInfo, preparedAttempt *relay.PreparedTextRelayAttempt) (*dto.Usage, *types.NewAPIError) {
				if preparedAttempt != nil {
					return relay.ExecutePreparedTextRelayAttempt(contextValue, relayInfo, preparedAttempt)
				}
				return relay.ExecuteTextAttemptWithoutQuota(contextValue, relayInfo)
			},
			settle: relay.SettleTextRelayUsage,
			beforeSettle: func(contextValue *gin.Context, relayInfo *relaycommon.RelayInfo, responseBuffer *managedResponseBuffer, output contextconsensus.ManagedAssistantOutput) error {
				var providerStateCommit *contextconsensus.ManagedProviderStateCommit
				if attempt != nil && managedRequest.Protocol == types.RelayFormatOpenAIResponses && relayInfo.ChannelType == constant.ChannelTypeOpenAI {
					body, bodyErr := responseBuffer.Body()
					if bodyErr != nil {
						return bodyErr
					}
					report, reportErr := attempt.ExtractManagedProviderStateReport(responseBuffer.Status(), body)
					if reportErr != nil {
						return reportErr
					}
					preparedCommit, prepareErr := session.PrepareProviderStateCommitForOwner(contextValue.Request.Context(), managedRequest.Owner, report, contextconsensus.ManagedProviderFinalTarget{
						RelayFormat: relayInfo.GetFinalRequestRelayFormat(), ChannelID: relayInfo.ChannelId, ChannelType: relayInfo.ChannelType,
						OriginModel: relayInfo.OriginModelName, UpstreamModel: relayInfo.FinalRequestModel, MultiKeyIndex: relayInfo.ChannelMultiKeyIndex,
						ChannelIsMultiKey: relayInfo.ChannelIsMultiKey, Credential: relayInfo.ApiKey,
					}, stateTTL, time.Now())
					if prepareErr != nil {
						return prepareErr
					}
					providerStateCommit = &preparedCommit
				}
				evidence = contextconsensus.ManagedRevisionEvidence{
					Protocol: managedRequest.Protocol, PreviousState: previousState,
					IncrementalSourceDigest: managedRequest.IncrementalSourceDigest,
					CurrentUserText:         managedRequest.CurrentUserText, AssistantOutput: output,
					PolicyVersion: policy.PolicyVersion, ProviderStateCommit: providerStateCommit,
				}
				var buildErr error
				plan, buildErr = contextconsensus.BuildManagedRevisionPlan(evidence)
				if buildErr != nil {
					return buildErr
				}
				summaryRequest, buildErr = contextconsensus.BuildManagedRevisionPrompt(compactionModel, evidence, plan, settings.MaxSummaryTokens)
				if buildErr != nil {
					return buildErr
				}
				summarySnapshot = contextconsensus.ManagedSummaryExecutionSnapshot{
					Evidence: evidence, Plan: plan, SummaryRequest: summaryRequest, CompactionModel: compactionModel,
					ModelPool: append([]string(nil), settings.CompactionModelPool...), AllowedChannelIDs: append([]int(nil), settings.CompactionChannelIDs...),
					MaximumSummaryTokens: settings.MaxSummaryTokens, MaximumInputTokens: settings.MaxCompactionInputTokens,
					MaximumQuota: settings.MaxCompactionQuota, TimeoutSeconds: settings.CompactionTimeoutSeconds,
				}
				body, bodyErr := responseBuffer.Body()
				if bodyErr != nil {
					return bodyErr
				}
				checkpoint, checkpointErr := outcome.MainCheckpoint(contextValue.Request.Context(), contextconsensus.ManagedOutcomeResponse{
					Status: responseBuffer.Status(), ContentType: responseBuffer.Header().Get("Content-Type"), Body: body,
				}, output, summarySnapshot)
				if checkpointErr != nil {
					return checkpointErr
				}
				relayInfo.ManagedOutcomeCheckpoint = managedOutcomeRelayCheckpoint(checkpoint)
				return nil
			},
		},
	)
	if finishMainAttempt != nil {
		finishMainAttempt()
	}
	if newAPIError != nil {
		return true, newAPIError
	}
	if err := outcome.Reload(c.Request.Context()); err != nil || outcome.Phase() != contextconsensus.ManagedOutcomePhaseMainSettled {
		return true, managedExecutionError(fmt.Errorf("managed context main outcome checkpoint is unavailable"))
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
		BillingOperationSeed: &managedBillingOperationSeed{
			Candidates:       managedRequest.BillingLookupCandidates,
			ExpectedRevision: managedRequest.ExpectedRevision,
			Purpose:          managedBillingPurposeSummary,
			Protocol:         managedRequest.Protocol,
			SourceDigest:     plan.SourceDigest,
			PolicyVersion:    policy.PolicyVersion,
		},
		MaxQuota:               settings.MaxCompactionQuota,
		Timeout:                time.Duration(settings.CompactionTimeoutSeconds) * time.Second,
		MaxResponseBytes:       defaultInternalCompactionResponseBytes,
		ManagedOutcome:         outcome,
		ManagedSummarySnapshot: &summarySnapshot,
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
	if err := outcome.Reload(c.Request.Context()); err != nil || outcome.Phase() != contextconsensus.ManagedOutcomePhaseSettledPendingCommit {
		return true, managedExecutionError(fmt.Errorf("managed context summary outcome checkpoint is unavailable"))
	}
	return true, commitAndFlushManagedRevision(c, session, plan, *childResult.Summary, mainResult.buffer, stateTTL, time.Now(), outcome)
}

func managedOutcomeRelayCheckpoint(checkpoint *model.ManagedContextOutcomeCheckpoint) *relaycommon.ManagedOutcomeBillingCheckpoint {
	if checkpoint == nil {
		return nil
	}
	return &relaycommon.ManagedOutcomeBillingCheckpoint{
		OutcomeId: checkpoint.OutcomeId, RequestFingerprint: checkpoint.RequestFingerprint,
		ExpectedPhase: checkpoint.ExpectedPhase, NextPhase: checkpoint.NextPhase,
		ResponseStatus: checkpoint.ResponseStatus, ResponseContentType: checkpoint.ResponseContentType,
		ResponsePayload: checkpoint.ResponsePayload, AssistantPayload: checkpoint.AssistantPayload,
		SummaryExecutionPayload: checkpoint.SummaryExecutionPayload, NextStatePayload: checkpoint.NextStatePayload,
		SummaryResultPayload: checkpoint.SummaryResultPayload,
	}
}

func commitAndFlushManagedRevision(
	c *gin.Context,
	committer managedRevisionCommitter,
	plan contextconsensus.ManagedRevisionPlan,
	summary contextconsensus.ConsensusSummary,
	buffer *managedResponseBuffer,
	stateTTL time.Duration,
	now time.Time,
	outcomes ...*contextconsensus.ManagedOutcomeSession,
) *types.NewAPIError {
	if c == nil || c.Request == nil || committer == nil || buffer == nil {
		return managedExecutionError(fmt.Errorf("managed consensus commit barrier is incomplete"))
	}
	var nextState contextconsensus.ManagedConsensusState
	var err error
	if len(outcomes) > 0 && outcomes[0] != nil {
		nextState, err = outcomes[0].NextState(c.Request.Context())
	} else {
		nextState, err = contextconsensus.BuildNextManagedConsensusState(plan, summary, now)
	}
	if err != nil {
		return managedExecutionError(err)
	}
	if plan.ProviderStateCommit != nil {
		providerCommitter, ok := committer.(managedProviderStateRevisionCommitter)
		if !ok {
			return managedExecutionError(fmt.Errorf("managed provider state commit barrier is unavailable"))
		}
		_, err = providerCommitter.CommitWithProviderStateRecovery(c.Request.Context(), nextState, *plan.ProviderStateCommit, stateTTL)
	} else {
		_, err = committer.CommitWithRecovery(c.Request.Context(), nextState, stateTTL)
	}
	if err != nil {
		return managedCommitAPIError(err)
	}
	if len(outcomes) > 0 && outcomes[0] != nil {
		if err := outcomes[0].MarkCommitted(c.Request.Context()); err != nil {
			return managedCommitAPIError(fmt.Errorf("persist managed context committed outcome: %w", err))
		}
		response, err := outcomes[0].Response(c.Request.Context())
		if err != nil {
			return managedExecutionError(fmt.Errorf("load committed managed context response: %w", err))
		}
		writeManagedOutcomeResponse(c, nextState.Revision, response)
		return nil
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
