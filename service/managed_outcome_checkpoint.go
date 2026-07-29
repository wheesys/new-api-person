package service

import (
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func managedOutcomeCheckpoint(checkpoint *relaycommon.ManagedOutcomeBillingCheckpoint) *model.ManagedContextOutcomeCheckpoint {
	if checkpoint == nil {
		return nil
	}
	return &model.ManagedContextOutcomeCheckpoint{
		OutcomeId: checkpoint.OutcomeId, RequestFingerprint: checkpoint.RequestFingerprint,
		ExpectedPhase: checkpoint.ExpectedPhase, NextPhase: checkpoint.NextPhase,
		ResponseStatus: checkpoint.ResponseStatus, ResponseContentType: checkpoint.ResponseContentType,
		ResponsePayload: checkpoint.ResponsePayload, AssistantPayload: checkpoint.AssistantPayload,
		SummaryExecutionPayload: checkpoint.SummaryExecutionPayload, NextStatePayload: checkpoint.NextStatePayload,
		SummaryResultPayload: checkpoint.SummaryResultPayload,
	}
}
