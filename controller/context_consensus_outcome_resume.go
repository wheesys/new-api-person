package controller

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/gin-gonic/gin"
)

func resumeManagedContextOutcome(c *gin.Context, request contextconsensus.ManagedContextRequest) (bool, *types.NewAPIError) {
	outcome, found := common.GetContextKeyType[*contextconsensus.ManagedOutcomeSession](c, constant.ContextKeyManagedContextOutcome)
	if !found || outcome == nil || outcome.Phase() == contextconsensus.ManagedOutcomePhaseIntent {
		return false, nil
	}
	if outcome.Phase() == contextconsensus.ManagedOutcomePhaseCommitted {
		response, err := outcome.Response(c.Request.Context())
		if err != nil {
			return true, managedExecutionError(err)
		}
		writeManagedOutcomeResponse(c, request.ExpectedRevision+1, response)
		return true, nil
	}
	consensusSession, found := common.GetContextKeyType[*contextconsensus.ManagedConsensusSession](c, constant.ContextKeyManagedContextSession)
	if !found || consensusSession == nil {
		return true, managedExecutionError(fmt.Errorf("managed consensus recovery session is unavailable"))
	}
	guard, found := common.GetContextKeyType[*contextconsensus.ManagedConsensusLeaseGuard](c, constant.ContextKeyManagedContextLease)
	if !found || guard == nil || errors.Join(c.Request.Context().Err(), guard.RenewalError()) != nil {
		return true, managedExecutionError(fmt.Errorf("managed consensus recovery lease is unavailable"))
	}
	if outcome.Phase() == contextconsensus.ManagedOutcomePhaseMainSettled {
		snapshot, err := outcome.SummaryExecution(c.Request.Context())
		if err != nil {
			return true, managedExecutionError(err)
		}
		executor, err := NewInternalCompactionExecutor(InternalCompactionExecutorRequest{
			ParentContext: c, Model: snapshot.CompactionModel, ModelPool: snapshot.ModelPool,
			AllowedChannelIDs: snapshot.AllowedChannelIDs, PolicyVersion: snapshot.Evidence.PolicyVersion,
			SourceDigest: snapshot.Plan.SourceDigest, MaxOutputTokens: snapshot.MaximumSummaryTokens,
			MaxInputTokens: snapshot.MaximumInputTokens, SummaryRequest: snapshot.SummaryRequest,
			Plan:                 contextconsensus.CompactionPlan{Protocol: request.Protocol, SourceDigest: snapshot.Plan.SourceDigest, MaxSummaryTokens: snapshot.MaximumSummaryTokens, PolicyVersion: snapshot.Evidence.PolicyVersion},
			ManagedRevisionPlan:  &snapshot.Plan,
			BillingOperationSeed: &managedBillingOperationSeed{Candidates: request.BillingLookupCandidates, ExpectedRevision: request.ExpectedRevision, Purpose: managedBillingPurposeSummary, Protocol: request.Protocol, SourceDigest: snapshot.Plan.SourceDigest, PolicyVersion: snapshot.Evidence.PolicyVersion},
			MaxQuota:             snapshot.MaximumQuota, Timeout: time.Duration(snapshot.TimeoutSeconds) * time.Second,
			MaxResponseBytes: defaultInternalCompactionResponseBytes, ManagedOutcome: outcome, ManagedSummarySnapshot: &snapshot,
		})
		if err != nil {
			return true, managedExecutionError(err)
		}
		childResult, err := executor.Execute(c.Request.Context())
		if err != nil || !childResult.Succeeded || childResult.Summary == nil {
			if err == nil {
				err = fmt.Errorf("managed summary recovery returned no committed summary")
			}
			return true, managedExecutionError(err)
		}
		if err := outcome.Reload(c.Request.Context()); err != nil {
			return true, managedExecutionError(err)
		}
	}
	if outcome.Phase() != contextconsensus.ManagedOutcomePhaseSettledPendingCommit {
		return true, managedExecutionError(fmt.Errorf("managed context outcome phase cannot be resumed"))
	}
	nextState, err := outcome.NextState(c.Request.Context())
	if err != nil {
		return true, managedExecutionError(err)
	}
	stateTTL := time.Until(outcome.Record().ExpiresAt)
	if stateTTL <= 0 {
		return true, managedExecutionError(fmt.Errorf("managed context outcome expired"))
	}
	if _, err := consensusSession.CommitWithRecovery(c.Request.Context(), nextState, stateTTL); err != nil {
		return true, managedCommitAPIError(err)
	}
	if err := outcome.MarkCommitted(c.Request.Context()); err != nil {
		return true, managedCommitAPIError(err)
	}
	response, err := outcome.Response(c.Request.Context())
	if err != nil {
		return true, managedExecutionError(err)
	}
	writeManagedOutcomeResponse(c, nextState.Revision, response)
	return true, nil
}

func writeManagedOutcomeResponse(c *gin.Context, revision uint64, response contextconsensus.ManagedOutcomeResponse) {
	for key := range c.Writer.Header() {
		c.Writer.Header().Del(key)
	}
	c.Header("Content-Type", response.ContentType)
	c.Header("Content-Length", strconv.Itoa(len(response.Body)))
	c.Header("X-New-Api-Context-Revision", strconv.FormatUint(revision, 10))
	c.Data(response.Status, response.ContentType, response.Body)
}
