package contextconsensus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

type CompactionChildState string

const (
	CompactionRequestPurposeContextCompaction = "context_compaction"

	CompactionChildStateReady            CompactionChildState = "ready"
	CompactionChildStatePreparing        CompactionChildState = "preparing"
	CompactionChildStatePrepared         CompactionChildState = "prepared"
	CompactionChildStatePreconsuming     CompactionChildState = "preconsuming"
	CompactionChildStatePreconsumed      CompactionChildState = "preconsumed"
	CompactionChildStateExecuting        CompactionChildState = "executing"
	CompactionChildStateExecuted         CompactionChildState = "executed"
	CompactionChildStateSettling         CompactionChildState = "settling"
	CompactionChildStateSettled          CompactionChildState = "settled"
	CompactionChildStateLogged           CompactionChildState = "logged"
	CompactionChildStateAuditFailed      CompactionChildState = "audit_failed"
	CompactionChildStateSettlementFailed CompactionChildState = "settlement_failed"
	CompactionChildStateRejected         CompactionChildState = "rejected"
	CompactionChildStateRefunding        CompactionChildState = "refunding"
	CompactionChildStateRefunded         CompactionChildState = "refunded"
	CompactionChildStateFailed           CompactionChildState = "failed"
)

type CompactionChildRequest struct {
	ParentRequestID string
	Model           string
	PolicyVersion   string
	SourceDigest    string
	MaxOutputTokens int
}

type CompactionChildDescriptor struct {
	ParentRequestID string
	ChildRequestID  string
	Model           string
	PolicyVersion   string
	SourceDigest    string
	MaxOutputTokens int
}

type PreparedCompactionChild struct {
	PreparationID         string
	PreparedRequestDigest string
}

type CompactionUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (usage CompactionUsage) TotalTokens() int {
	return usage.InputTokens + usage.OutputTokens
}

type CompactionExecutionOutput struct {
	Summary       ConsensusSummary
	SummaryDigest string
	Usage         CompactionUsage
}

// BillableCompactionExecutionError reports a failed execution that still
// produced valid output and usage which must be settled.
type BillableCompactionExecutionError struct {
	output CompactionExecutionOutput
	cause  error
}

// NewBillableCompactionExecutionError marks valid execution output for settlement despite failure.
func NewBillableCompactionExecutionError(output CompactionExecutionOutput, cause error) *BillableCompactionExecutionError {
	return &BillableCompactionExecutionError{output: output, cause: cause}
}

func (executionError *BillableCompactionExecutionError) Error() string {
	if executionError == nil {
		return "billable compaction execution failed"
	}
	if executionError.cause == nil {
		return "billable compaction execution failed"
	}
	return fmt.Sprintf("billable compaction execution failed: %v", executionError.cause)
}

func (executionError *BillableCompactionExecutionError) Unwrap() error {
	if executionError == nil {
		return nil
	}
	return executionError.cause
}

// ExecutionOutput returns the output that must be used for settlement.
func (executionError *BillableCompactionExecutionError) ExecutionOutput() CompactionExecutionOutput {
	if executionError == nil {
		return CompactionExecutionOutput{}
	}
	return executionError.output
}

// AsBillableCompactionExecutionError recognizes the contract through wrapped errors.
func AsBillableCompactionExecutionError(err error) (*BillableCompactionExecutionError, bool) {
	var executionError *BillableCompactionExecutionError
	if !errors.As(err, &executionError) || executionError == nil {
		return nil, false
	}
	return executionError, true
}

type CompactionBillingReceipt struct {
	BillingReference string
	ReservedQuota    int
}

type CompactionSettlement struct {
	SettledQuota int
}

type CompactionAuditRecord struct {
	RequestPurpose           string               `json:"request_purpose"`
	ParentRequestID          string               `json:"parent_request_id"`
	ChildRequestID           string               `json:"child_request_id"`
	Model                    string               `json:"model"`
	PolicyVersion            string               `json:"policy_version"`
	SourceDigest             string               `json:"source_digest"`
	PreparedRequestDigest    string               `json:"prepared_request_digest,omitempty"`
	SummaryDigest            string               `json:"summary_digest,omitempty"`
	State                    CompactionChildState `json:"state"`
	ResultCode               string               `json:"result_code"`
	InputTokens              int                  `json:"input_tokens"`
	OutputTokens             int                  `json:"output_tokens"`
	TotalTokens              int                  `json:"total_tokens"`
	MaxOutputTokens          int                  `json:"max_output_tokens"`
	ReservedQuota            int                  `json:"reserved_quota"`
	SettledQuota             int                  `json:"settled_quota"`
	Refunded                 bool                 `json:"refunded"`
	BillableExecutionFailure bool                 `json:"billable_execution_failure"`
}

type CompactionChildResult struct {
	ParentRequestID          string
	ChildRequestID           string
	Model                    string
	State                    CompactionChildState
	ResultCode               string
	Succeeded                bool
	PreparedRequestDigest    string
	SummaryDigest            string
	Summary                  *ConsensusSummary
	Usage                    CompactionUsage
	ReservedQuota            int
	SettledQuota             int
	Refunded                 bool
	BillableExecutionFailure bool
	AuditRecorded            bool
}

type CompactionChildRequestIDGenerator interface {
	NewChildRequestID(parentRequestID string) (string, error)
}

type CompactionChildPreparer interface {
	PrepareCompactionChild(ctx context.Context, descriptor CompactionChildDescriptor) (PreparedCompactionChild, error)
}

type CompactionChildBilling interface {
	PreconsumeCompactionChild(ctx context.Context, descriptor CompactionChildDescriptor, prepared PreparedCompactionChild) (*CompactionBillingReceipt, error)
	NeedsRefund(receipt *CompactionBillingReceipt) bool
	SettleCompactionChild(ctx context.Context, receipt *CompactionBillingReceipt, output CompactionExecutionOutput) (CompactionSettlement, error)
	RefundCompactionChild(ctx context.Context, receipt *CompactionBillingReceipt) error
}

type CompactionChildRunner interface {
	ExecuteCompactionChild(ctx context.Context, descriptor CompactionChildDescriptor, prepared PreparedCompactionChild) (CompactionExecutionOutput, error)
}

type CompactionChildAuditor interface {
	RecordCompactionChild(ctx context.Context, record CompactionAuditRecord) error
}

type CompactionChildDependencies struct {
	RequestIDGenerator CompactionChildRequestIDGenerator
	Preparer           CompactionChildPreparer
	Billing            CompactionChildBilling
	Runner             CompactionChildRunner
	Auditor            CompactionChildAuditor
}

type CompactionChildExecutor struct {
	dependencies CompactionChildDependencies
	mutex        sync.Mutex
	state        CompactionChildState
	started      bool
}

func NewCompactionChildExecutor(dependencies CompactionChildDependencies) (*CompactionChildExecutor, error) {
	if dependencies.RequestIDGenerator == nil {
		return nil, fmt.Errorf("compaction child request ID generator is required")
	}
	if dependencies.Preparer == nil {
		return nil, fmt.Errorf("compaction child preparer is required")
	}
	if dependencies.Billing == nil {
		return nil, fmt.Errorf("compaction child billing lifecycle is required")
	}
	if dependencies.Runner == nil {
		return nil, fmt.Errorf("compaction child runner is required")
	}
	if dependencies.Auditor == nil {
		return nil, fmt.Errorf("compaction child auditor is required")
	}
	return &CompactionChildExecutor{
		dependencies: dependencies,
		state:        CompactionChildStateReady,
	}, nil
}

func ValidateExplicitCompactionModel(model string) error {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return fmt.Errorf("compaction model is required")
	}
	if normalized == "auto" || normalized == "smart" || strings.HasPrefix(normalized, "auto:") || strings.HasPrefix(normalized, "smart:") {
		return fmt.Errorf("compaction model must be an explicit real model")
	}
	return nil
}

func (executor *CompactionChildExecutor) State() CompactionChildState {
	if executor == nil {
		return CompactionChildStateFailed
	}
	executor.mutex.Lock()
	defer executor.mutex.Unlock()
	return executor.state
}

func (executor *CompactionChildExecutor) Execute(ctx context.Context, request CompactionChildRequest) (CompactionChildResult, error) {
	if executor == nil {
		return CompactionChildResult{State: CompactionChildStateFailed, ResultCode: "executor_unavailable"}, fmt.Errorf("compaction child executor is nil")
	}
	if !executor.begin() {
		return CompactionChildResult{State: executor.State(), ResultCode: "already_executed"}, fmt.Errorf("compaction child executor can only execute once")
	}

	result := CompactionChildResult{
		ParentRequestID: request.ParentRequestID,
		Model:           strings.TrimSpace(request.Model),
	}
	if strings.TrimSpace(request.ParentRequestID) == "" {
		return executor.fail(ctx, request, result, "invalid_parent_request_id", nil, fmt.Errorf("parent request ID is required"))
	}
	if err := ValidateExplicitCompactionModel(request.Model); err != nil {
		return executor.fail(ctx, request, result, "invalid_compaction_model", nil, err)
	}
	if strings.TrimSpace(request.PolicyVersion) == "" {
		return executor.fail(ctx, request, result, "invalid_policy_version", nil, fmt.Errorf("compaction policy version is required"))
	}
	if strings.TrimSpace(request.SourceDigest) == "" {
		return executor.fail(ctx, request, result, "invalid_source_digest", nil, fmt.Errorf("compaction source digest is required"))
	}
	if request.MaxOutputTokens <= 0 {
		return executor.fail(ctx, request, result, "invalid_max_output_tokens", nil, fmt.Errorf("compaction max output tokens must be positive"))
	}

	childRequestID, err := executor.dependencies.RequestIDGenerator.NewChildRequestID(request.ParentRequestID)
	if err != nil {
		return executor.fail(ctx, request, result, "request_id_failed", nil, err)
	}
	childRequestID = strings.TrimSpace(childRequestID)
	result.ChildRequestID = childRequestID
	if childRequestID == "" || childRequestID == request.ParentRequestID {
		return executor.fail(ctx, request, result, "invalid_child_request_id", nil, fmt.Errorf("child request ID must be non-empty and independent from parent request ID"))
	}

	descriptor := CompactionChildDescriptor{
		ParentRequestID: request.ParentRequestID,
		ChildRequestID:  childRequestID,
		Model:           strings.TrimSpace(request.Model),
		PolicyVersion:   strings.TrimSpace(request.PolicyVersion),
		SourceDigest:    strings.TrimSpace(request.SourceDigest),
		MaxOutputTokens: request.MaxOutputTokens,
	}
	executor.setState(CompactionChildStatePreparing)
	prepared, err := executor.dependencies.Preparer.PrepareCompactionChild(ctx, descriptor)
	result.PreparedRequestDigest = prepared.PreparedRequestDigest
	if err != nil {
		return executor.fail(ctx, request, result, "prepare_failed", nil, err)
	}
	if strings.TrimSpace(prepared.PreparationID) == "" || strings.TrimSpace(prepared.PreparedRequestDigest) == "" {
		return executor.fail(ctx, request, result, "invalid_prepared_request", nil, fmt.Errorf("prepared compaction child identifiers are required"))
	}
	executor.setState(CompactionChildStatePrepared)

	executor.setState(CompactionChildStatePreconsuming)
	receipt, err := executor.dependencies.Billing.PreconsumeCompactionChild(ctx, descriptor, prepared)
	if receipt != nil {
		result.ReservedQuota = receipt.ReservedQuota
	}
	if err != nil {
		return executor.fail(ctx, request, result, "preconsume_failed", receipt, err)
	}
	if receipt == nil || strings.TrimSpace(receipt.BillingReference) == "" {
		return executor.fail(ctx, request, result, "invalid_billing_receipt", receipt, fmt.Errorf("compaction billing receipt is required"))
	}
	executor.setState(CompactionChildStatePreconsumed)

	executor.setState(CompactionChildStateExecuting)
	output, err := executor.dependencies.Runner.ExecuteCompactionChild(ctx, descriptor, prepared)
	if err != nil {
		billableExecutionError, billable := AsBillableCompactionExecutionError(err)
		if billable {
			output = billableExecutionError.ExecutionOutput()
			result.SummaryDigest = output.SummaryDigest
			result.Usage = output.Usage
			validationCode, validationErr := validateCompactionExecutionOutput(output)
			if validationErr != nil {
				return executor.fail(ctx, request, result, validationCode, receipt, errors.Join(err, validationErr))
			}
			return executor.finishBillableExecutionFailure(ctx, request, result, receipt, output, err)
		}
		result.SummaryDigest = output.SummaryDigest
		result.Usage = output.Usage
		return executor.fail(ctx, request, result, "execute_failed", receipt, err)
	}
	result.SummaryDigest = output.SummaryDigest
	result.Usage = output.Usage
	if validationCode, validationErr := validateCompactionExecutionOutput(output); validationErr != nil {
		return executor.fail(ctx, request, result, validationCode, receipt, validationErr)
	}
	executor.setState(CompactionChildStateExecuted)

	executor.setState(CompactionChildStateSettling)
	settlement, err := executor.dependencies.Billing.SettleCompactionChild(ctx, receipt, output)
	result.SettledQuota = settlement.SettledQuota
	if err != nil {
		return executor.fail(ctx, request, result, "settle_failed", receipt, err)
	}
	executor.setState(CompactionChildStateSettled)
	result.State = CompactionChildStateSettled
	result.ResultCode = "success"
	result.Succeeded = true
	result.Summary = &output.Summary

	result.State = CompactionChildStateLogged
	auditErr := executor.dependencies.Auditor.RecordCompactionChild(ctx, buildCompactionAuditRecord(request, result))
	if auditErr != nil {
		result.Succeeded = false
		result.ResultCode = "audit_failed"
		executor.setState(CompactionChildStateAuditFailed)
		result.State = CompactionChildStateAuditFailed
		return result, fmt.Errorf("record compaction child audit after settlement: %w", auditErr)
	}
	executor.setState(CompactionChildStateLogged)
	result.AuditRecorded = true
	return result, nil
}

func (executor *CompactionChildExecutor) finishBillableExecutionFailure(
	ctx context.Context,
	request CompactionChildRequest,
	result CompactionChildResult,
	receipt *CompactionBillingReceipt,
	output CompactionExecutionOutput,
	executionErr error,
) (CompactionChildResult, error) {
	result.BillableExecutionFailure = true
	result.Succeeded = false
	result.Summary = &output.Summary
	executor.setState(CompactionChildStateExecuted)

	executor.setState(CompactionChildStateSettling)
	settlement, settlementErr := executor.dependencies.Billing.SettleCompactionChild(ctx, receipt, output)
	result.SettledQuota = settlement.SettledQuota
	if settlementErr != nil {
		executor.setState(CompactionChildStateSettlementFailed)
		result.State = CompactionChildStateSettlementFailed
		result.ResultCode = "billable_execution_settle_failed"
		auditErr := executor.dependencies.Auditor.RecordCompactionChild(ctx, buildCompactionAuditRecord(request, result))
		result.AuditRecorded = auditErr == nil
		return result, errors.Join(executionErr, fmt.Errorf("settle billable compaction execution: %w", settlementErr), auditErr)
	}

	executor.setState(CompactionChildStateSettled)
	result.State = CompactionChildStateLogged
	result.ResultCode = "billable_execution_failed"
	auditErr := executor.dependencies.Auditor.RecordCompactionChild(ctx, buildCompactionAuditRecord(request, result))
	if auditErr != nil {
		executor.setState(CompactionChildStateAuditFailed)
		result.State = CompactionChildStateAuditFailed
		result.ResultCode = "billable_execution_audit_failed"
		return result, errors.Join(executionErr, fmt.Errorf("record billable compaction execution audit after settlement: %w", auditErr))
	}
	executor.setState(CompactionChildStateLogged)
	result.AuditRecorded = true
	return result, executionErr
}

func validateCompactionExecutionOutput(output CompactionExecutionOutput) (string, error) {
	if strings.TrimSpace(output.SummaryDigest) == "" {
		return "invalid_execution_result", fmt.Errorf("compaction summary digest is required")
	}
	if output.Usage.InputTokens < 0 || output.Usage.OutputTokens < 0 {
		return "invalid_execution_usage", fmt.Errorf("compaction usage tokens must not be negative")
	}
	return "", nil
}

func (executor *CompactionChildExecutor) begin() bool {
	executor.mutex.Lock()
	defer executor.mutex.Unlock()
	if executor.started {
		return false
	}
	executor.started = true
	return true
}

func (executor *CompactionChildExecutor) setState(state CompactionChildState) {
	executor.mutex.Lock()
	executor.state = state
	executor.mutex.Unlock()
}

func (executor *CompactionChildExecutor) fail(
	ctx context.Context,
	request CompactionChildRequest,
	result CompactionChildResult,
	resultCode string,
	receipt *CompactionBillingReceipt,
	cause error,
) (CompactionChildResult, error) {
	result.Succeeded = false
	result.ResultCode = resultCode
	refundErr := executor.refundIfNeeded(ctx, receipt, &result)
	failureState := CompactionChildStateFailed
	if strings.HasPrefix(resultCode, "invalid_") {
		failureState = CompactionChildStateRejected
	} else if resultCode == "settle_failed" {
		failureState = CompactionChildStateSettlementFailed
	}
	executor.setState(failureState)
	if result.Refunded {
		executor.setState(CompactionChildStateRefunded)
	}
	result.State = executor.State()
	auditErr := executor.dependencies.Auditor.RecordCompactionChild(ctx, buildCompactionAuditRecord(request, result))
	result.AuditRecorded = auditErr == nil
	return result, errors.Join(cause, refundErr, auditErr)
}

func (executor *CompactionChildExecutor) refundIfNeeded(ctx context.Context, receipt *CompactionBillingReceipt, result *CompactionChildResult) error {
	if receipt == nil || !executor.dependencies.Billing.NeedsRefund(receipt) {
		return nil
	}
	executor.setState(CompactionChildStateRefunding)
	if err := executor.dependencies.Billing.RefundCompactionChild(ctx, receipt); err != nil {
		return fmt.Errorf("refund compaction child: %w", err)
	}
	result.Refunded = true
	return nil
}

func buildCompactionAuditRecord(request CompactionChildRequest, result CompactionChildResult) CompactionAuditRecord {
	return CompactionAuditRecord{
		RequestPurpose:           CompactionRequestPurposeContextCompaction,
		ParentRequestID:          result.ParentRequestID,
		ChildRequestID:           result.ChildRequestID,
		Model:                    result.Model,
		PolicyVersion:            strings.TrimSpace(request.PolicyVersion),
		SourceDigest:             strings.TrimSpace(request.SourceDigest),
		PreparedRequestDigest:    result.PreparedRequestDigest,
		SummaryDigest:            result.SummaryDigest,
		State:                    result.State,
		ResultCode:               result.ResultCode,
		InputTokens:              result.Usage.InputTokens,
		OutputTokens:             result.Usage.OutputTokens,
		TotalTokens:              result.Usage.TotalTokens(),
		MaxOutputTokens:          request.MaxOutputTokens,
		ReservedQuota:            result.ReservedQuota,
		SettledQuota:             result.SettledQuota,
		Refunded:                 result.Refunded,
		BillableExecutionFailure: result.BillableExecutionFailure,
	}
}
