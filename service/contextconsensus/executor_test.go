package contextconsensus

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testCompactionChildLifecycle struct {
	childRequestID        string
	failStage             string
	needsRefund           bool
	keepRefundAfterSettle bool
	order                 []string
	auditRecords          []CompactionAuditRecord
}

func (lifecycle *testCompactionChildLifecycle) NewChildRequestID(string) (string, error) {
	lifecycle.order = append(lifecycle.order, "request_id")
	if lifecycle.failStage == "request_id" {
		return "", errors.New("request ID failed")
	}
	return lifecycle.childRequestID, nil
}

func (lifecycle *testCompactionChildLifecycle) PrepareCompactionChild(_ context.Context, _ CompactionChildDescriptor) (PreparedCompactionChild, error) {
	lifecycle.order = append(lifecycle.order, "prepare")
	prepared := PreparedCompactionChild{PreparationID: "preparation-1", PreparedRequestDigest: "prepared-digest"}
	if lifecycle.failStage == "prepare" {
		return prepared, errors.New("prepare failed")
	}
	return prepared, nil
}

func (lifecycle *testCompactionChildLifecycle) PreconsumeCompactionChild(_ context.Context, _ CompactionChildDescriptor, _ PreparedCompactionChild) (*CompactionBillingReceipt, error) {
	lifecycle.order = append(lifecycle.order, "preconsume")
	receipt := &CompactionBillingReceipt{BillingReference: "billing-1", ReservedQuota: 12}
	if lifecycle.failStage == "preconsume" {
		return receipt, errors.New("preconsume failed")
	}
	return receipt, nil
}

func (lifecycle *testCompactionChildLifecycle) NeedsRefund(*CompactionBillingReceipt) bool {
	return lifecycle.needsRefund
}

func (lifecycle *testCompactionChildLifecycle) SettleCompactionChild(_ context.Context, _ *CompactionBillingReceipt, _ CompactionExecutionOutput) (CompactionSettlement, error) {
	lifecycle.order = append(lifecycle.order, "settle")
	if lifecycle.failStage == "settle" {
		return CompactionSettlement{SettledQuota: 9}, errors.New("settle failed")
	}
	if !lifecycle.keepRefundAfterSettle {
		lifecycle.needsRefund = false
	}
	return CompactionSettlement{SettledQuota: 9}, nil
}

func (lifecycle *testCompactionChildLifecycle) RefundCompactionChild(_ context.Context, _ *CompactionBillingReceipt) error {
	lifecycle.order = append(lifecycle.order, "refund")
	if lifecycle.failStage == "refund" {
		return errors.New("refund failed")
	}
	lifecycle.needsRefund = false
	return nil
}

func (lifecycle *testCompactionChildLifecycle) ExecuteCompactionChild(_ context.Context, _ CompactionChildDescriptor, _ PreparedCompactionChild) (CompactionExecutionOutput, error) {
	lifecycle.order = append(lifecycle.order, "execute")
	output := CompactionExecutionOutput{
		Summary:       ConsensusSummary{Version: ConsensusSummaryVersion},
		SummaryDigest: "summary-digest",
		Usage:         CompactionUsage{InputTokens: 100, OutputTokens: 20},
	}
	if lifecycle.failStage == "execute" {
		return output, errors.New("execute failed")
	}
	return output, nil
}

func (lifecycle *testCompactionChildLifecycle) RecordCompactionChild(_ context.Context, record CompactionAuditRecord) error {
	lifecycle.order = append(lifecycle.order, "audit")
	lifecycle.auditRecords = append(lifecycle.auditRecords, record)
	if lifecycle.failStage == "audit" {
		return errors.New("audit failed")
	}
	return nil
}

func TestCompactionChildExecutorRunsIndependentLifecycle(t *testing.T) {
	lifecycle := &testCompactionChildLifecycle{childRequestID: "child-request-1", needsRefund: true}
	executor := newTestCompactionChildExecutor(t, lifecycle)

	result, err := executor.Execute(context.Background(), validCompactionChildRequest())
	require.NoError(t, err)
	assert.True(t, result.Succeeded)
	assert.Equal(t, "parent-request-1", result.ParentRequestID)
	assert.Equal(t, "child-request-1", result.ChildRequestID)
	assert.NotEqual(t, result.ParentRequestID, result.ChildRequestID)
	assert.Equal(t, CompactionChildStateLogged, result.State)
	assert.Equal(t, "prepared-digest", result.PreparedRequestDigest)
	assert.Equal(t, "summary-digest", result.SummaryDigest)
	assert.Equal(t, CompactionUsage{InputTokens: 100, OutputTokens: 20}, result.Usage)
	assert.Equal(t, 12, result.ReservedQuota)
	assert.Equal(t, 9, result.SettledQuota)
	assert.True(t, result.AuditRecorded)
	assert.Equal(t, []string{"request_id", "prepare", "preconsume", "execute", "settle", "audit"}, lifecycle.order)

	require.Len(t, lifecycle.auditRecords, 1)
	auditRecord := lifecycle.auditRecords[0]
	assert.Equal(t, result.ParentRequestID, auditRecord.ParentRequestID)
	assert.Equal(t, result.ChildRequestID, auditRecord.ChildRequestID)
	assert.Equal(t, "gpt-5-mini", auditRecord.Model)
	assert.Equal(t, "context-compaction-v1", auditRecord.PolicyVersion)
	assert.Equal(t, "source-digest", auditRecord.SourceDigest)
	assert.Equal(t, "success", auditRecord.ResultCode)
	assert.Equal(t, CompactionChildStateLogged, auditRecord.State)
	assert.Equal(t, CompactionRequestPurposeContextCompaction, auditRecord.RequestPurpose)
	assert.Equal(t, 120, auditRecord.TotalTokens)
	assert.Equal(t, 256, auditRecord.MaxOutputTokens)
}

func TestCompactionChildExecutorRejectsVirtualModelsBeforePreparation(t *testing.T) {
	models := []string{"auto", "smart", "auto:quality", "SMART:fast"}
	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			lifecycle := &testCompactionChildLifecycle{childRequestID: "child-request-1"}
			executor := newTestCompactionChildExecutor(t, lifecycle)
			request := validCompactionChildRequest()
			request.Model = model

			result, err := executor.Execute(context.Background(), request)
			require.ErrorContains(t, err, "explicit real model")
			assert.Equal(t, "invalid_compaction_model", result.ResultCode)
			assert.False(t, result.Succeeded)
			assert.NotContains(t, lifecycle.order, "prepare")
			assert.Equal(t, CompactionChildStateRejected, result.State)
			assert.Equal(t, []string{"audit"}, lifecycle.order)
		})
	}
}

func TestCompactionChildExecutorRejectsInvalidOutputLimitBeforeAllocatingChildID(t *testing.T) {
	lifecycle := &testCompactionChildLifecycle{childRequestID: "child-request-1"}
	executor := newTestCompactionChildExecutor(t, lifecycle)
	request := validCompactionChildRequest()
	request.MaxOutputTokens = 0

	result, err := executor.Execute(context.Background(), request)
	require.ErrorContains(t, err, "must be positive")
	assert.Equal(t, "invalid_max_output_tokens", result.ResultCode)
	assert.Equal(t, CompactionChildStateRejected, result.State)
	assert.Empty(t, result.ChildRequestID)
	assert.Equal(t, []string{"audit"}, lifecycle.order)
}

func TestCompactionChildExecutorRejectsNonIndependentChildRequestID(t *testing.T) {
	tests := []struct {
		name           string
		childRequestID string
	}{
		{name: "empty", childRequestID: ""},
		{name: "same as parent", childRequestID: "parent-request-1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lifecycle := &testCompactionChildLifecycle{childRequestID: test.childRequestID}
			executor := newTestCompactionChildExecutor(t, lifecycle)

			result, err := executor.Execute(context.Background(), validCompactionChildRequest())
			require.ErrorContains(t, err, "must be non-empty and independent")
			assert.Equal(t, "invalid_child_request_id", result.ResultCode)
			assert.Equal(t, CompactionChildStateRejected, result.State)
			assert.NotContains(t, lifecycle.order, "prepare")
			assert.Equal(t, []string{"request_id", "audit"}, lifecycle.order)
		})
	}
}

func TestCompactionChildExecutorRefundsFailuresOnlyWhenNeeded(t *testing.T) {
	tests := []struct {
		name           string
		failStage      string
		needsRefund    bool
		expectedRefund bool
		expectedCode   string
	}{
		{name: "prepare has no receipt", failStage: "prepare", needsRefund: true, expectedCode: "prepare_failed"},
		{name: "preconsume returns refundable receipt", failStage: "preconsume", needsRefund: true, expectedRefund: true, expectedCode: "preconsume_failed"},
		{name: "execute refundable", failStage: "execute", needsRefund: true, expectedRefund: true, expectedCode: "execute_failed"},
		{name: "execute no refund needed", failStage: "execute", needsRefund: false, expectedCode: "execute_failed"},
		{name: "settle refundable", failStage: "settle", needsRefund: true, expectedRefund: true, expectedCode: "settle_failed"},
		{name: "settle no refund needed", failStage: "settle", needsRefund: false, expectedCode: "settle_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lifecycle := &testCompactionChildLifecycle{
				childRequestID: "child-request-1",
				failStage:      test.failStage,
				needsRefund:    test.needsRefund,
			}
			executor := newTestCompactionChildExecutor(t, lifecycle)

			result, err := executor.Execute(context.Background(), validCompactionChildRequest())
			require.Error(t, err)
			assert.Equal(t, test.expectedCode, result.ResultCode)
			assert.Equal(t, test.expectedRefund, result.Refunded)
			assert.Equal(t, test.expectedRefund, containsLifecycleStep(lifecycle.order, "refund"))
			if test.expectedRefund {
				assert.Equal(t, CompactionChildStateRefunded, result.State)
			} else {
				expectedState := CompactionChildStateFailed
				if test.failStage == "settle" {
					expectedState = CompactionChildStateSettlementFailed
				}
				assert.Equal(t, expectedState, result.State)
			}
			assert.True(t, result.AuditRecorded)
			assert.Equal(t, "audit", lifecycle.order[len(lifecycle.order)-1])
		})
	}
}

func TestCompactionChildExecutorCanOnlyRunOnce(t *testing.T) {
	lifecycle := &testCompactionChildLifecycle{childRequestID: "child-request-1"}
	executor := newTestCompactionChildExecutor(t, lifecycle)

	_, err := executor.Execute(context.Background(), validCompactionChildRequest())
	require.NoError(t, err)
	firstOrder := append([]string(nil), lifecycle.order...)
	result, err := executor.Execute(context.Background(), validCompactionChildRequest())
	require.ErrorContains(t, err, "only execute once")
	assert.Equal(t, "already_executed", result.ResultCode)
	assert.Equal(t, CompactionChildStateLogged, result.State)
	assert.Equal(t, firstOrder, lifecycle.order)
}

func TestCompactionChildExecutorAllowsOnlyOneConcurrentExecution(t *testing.T) {
	lifecycle := &testCompactionChildLifecycle{childRequestID: "child-request-1"}
	executor := newTestCompactionChildExecutor(t, lifecycle)
	start := make(chan struct{})
	type executionOutcome struct {
		result CompactionChildResult
		err    error
	}
	outcomes := make(chan executionOutcome, 16)

	var waitGroup sync.WaitGroup
	for i := 0; i < cap(outcomes); i++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			result, err := executor.Execute(context.Background(), validCompactionChildRequest())
			outcomes <- executionOutcome{result: result, err: err}
		}()
	}
	close(start)
	waitGroup.Wait()
	close(outcomes)

	successCount := 0
	alreadyExecutedCount := 0
	for outcome := range outcomes {
		if outcome.err == nil {
			successCount++
			continue
		}
		assert.Equal(t, "already_executed", outcome.result.ResultCode)
		alreadyExecutedCount++
	}
	assert.Equal(t, 1, successCount)
	assert.Equal(t, 15, alreadyExecutedCount)
	assert.Equal(t, []string{"request_id", "prepare", "preconsume", "execute", "settle", "audit"}, lifecycle.order)
}

func TestCompactionChildExecutorDoesNotRefundAfterSuccessfulSettlementWhenAuditFails(t *testing.T) {
	lifecycle := &testCompactionChildLifecycle{
		childRequestID:        "child-request-1",
		failStage:             "audit",
		needsRefund:           true,
		keepRefundAfterSettle: true,
	}
	executor := newTestCompactionChildExecutor(t, lifecycle)

	result, err := executor.Execute(context.Background(), validCompactionChildRequest())
	require.ErrorContains(t, err, "audit after settlement")
	assert.Equal(t, "audit_failed", result.ResultCode)
	assert.Equal(t, CompactionChildStateAuditFailed, result.State)
	assert.Equal(t, 9, result.SettledQuota)
	assert.False(t, result.Refunded)
	assert.False(t, result.AuditRecorded)
	assert.NotContains(t, lifecycle.order, "refund")
	assert.Equal(t, []string{"request_id", "prepare", "preconsume", "execute", "settle", "audit"}, lifecycle.order)
}

func newTestCompactionChildExecutor(t *testing.T, lifecycle *testCompactionChildLifecycle) *CompactionChildExecutor {
	t.Helper()
	executor, err := NewCompactionChildExecutor(CompactionChildDependencies{
		RequestIDGenerator: lifecycle,
		Preparer:           lifecycle,
		Billing:            lifecycle,
		Runner:             lifecycle,
		Auditor:            lifecycle,
	})
	require.NoError(t, err)
	return executor
}

func validCompactionChildRequest() CompactionChildRequest {
	return CompactionChildRequest{
		ParentRequestID: "parent-request-1",
		Model:           "gpt-5-mini",
		PolicyVersion:   "context-compaction-v1",
		SourceDigest:    "source-digest",
		MaxOutputTokens: 256,
	}
}

func containsLifecycleStep(steps []string, expected string) bool {
	for _, step := range steps {
		if step == expected {
			return true
		}
	}
	return false
}
