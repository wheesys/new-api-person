package controller

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInternalCompactionExecutorIsolatesChildRequest(t *testing.T) {
	parent, parentRecorder := newInternalCompactionParent(t)
	parent.Request.Header.Set("Authorization", "Bearer parent-secret")
	parent.Request.Header.Set("Cookie", "session=parent-secret")
	common.SetContextKey(parent, constant.ContextKeyChannelKey, "parent-channel-secret")
	common.SetContextKey(parent, constant.ContextKeySmartRoutingDecision, "parent-routing-state")

	request, summaryBody := validInternalCompactionRequest(t, parent)
	var capturedChild *gin.Context
	var capturedInfo *relaycommon.RelayInfo
	postConsumeCalls := 0
	dependencies := successfulInternalCompactionDependencies(t, summaryBody)
	dependencies.selectChannel = func(ctx *gin.Context, modelName, tokenGroup string) (*model.Channel, string, error) {
		capturedChild = ctx
		assert.Equal(t, "summary-model", modelName)
		assert.Equal(t, "default", tokenGroup)
		return &model.Channel{Id: 9, Name: "summary", Type: 1, Key: "child-channel-secret"}, "default", nil
	}
	dependencies.executeAttempt = func(ctx *gin.Context, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
		capturedInfo = info
		_, err := ctx.Writer.Write(openAICompactionResponse(t, summaryBody))
		require.NoError(t, err)
		return &dto.Usage{PromptTokens: 30, CompletionTokens: 12}, nil
	}
	dependencies.postConsume = func(_ *gin.Context, _ *relaycommon.RelayInfo, usage *dto.Usage) (service.TextConsumeResult, error) {
		postConsumeCalls++
		return service.TextConsumeResult{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: 42, ActualQuota: 84, LogRecorded: true}, nil
	}

	executor, err := newInternalCompactionExecutor(request, dependencies)
	require.NoError(t, err)
	result, err := executor.Execute(context.Background())
	require.NoError(t, err)
	require.True(t, result.Succeeded)
	assert.Equal(t, contextconsensus.CompactionChildStateLogged, result.State)
	assert.Equal(t, "parent-request", result.ParentRequestID)
	assert.Equal(t, "child-request", result.ChildRequestID)
	assert.Equal(t, 84, result.SettledQuota)
	assert.Equal(t, 1, postConsumeCalls)

	require.NotNil(t, capturedChild)
	require.NotNil(t, capturedInfo)
	assert.NotSame(t, parent, capturedChild)
	assert.NotSame(t, parent.Request, capturedChild.Request)
	assert.Equal(t, "", capturedChild.Request.Header.Get("Authorization"))
	assert.Equal(t, "", capturedChild.Request.Header.Get("Cookie"))
	assert.Equal(t, "application/json", capturedChild.Request.Header.Get("Content-Type"))
	assert.Equal(t, "application/json", capturedChild.Request.Header.Get("Accept"))
	_, hasParentChannelKey := common.GetContextKey(capturedChild, constant.ContextKeyChannelKey)
	assert.True(t, hasParentChannelKey, "the selected child channel must install its own key")
	assert.Equal(t, "child-channel-secret", common.GetContextKeyString(capturedChild, constant.ContextKeyChannelKey))
	_, hasSmartRouting := common.GetContextKey(capturedChild, constant.ContextKeySmartRoutingDecision)
	assert.False(t, hasSmartRouting)
	assert.True(t, common.GetContextKeyBool(capturedChild, constant.ContextKeySuppressDebugLog))
	assert.Equal(t, "child-request", capturedInfo.RequestId)
	assert.Equal(t, "parent-request", capturedInfo.ParentRequestId)
	assert.Equal(t, contextconsensus.CompactionRequestPurposeContextCompaction, capturedInfo.RequestPurpose)
	assert.Equal(t, "policy-v1", capturedInfo.PolicyVersion)
	assert.False(t, capturedInfo.IsStream)
	require.NotNil(t, capturedInfo.BillingRequestInput)
	assert.NotContains(t, string(capturedInfo.BillingRequestInput.Body), "parent-secret")
	assert.Equal(t, 0, parentRecorder.Body.Len())
	assert.Equal(t, "parent-channel-secret", common.GetContextKeyString(parent, constant.ContextKeyChannelKey))
}

func TestInternalCompactionExecutorSettlesInvalidBillableSummary(t *testing.T) {
	parent, _ := newInternalCompactionParent(t)
	request, _ := validInternalCompactionRequest(t, parent)
	billing := &fakeInternalCompactionBilling{needsRefund: true, preConsumedQuota: 25}
	postConsumeCalls := 0
	recordErrorCalls := 0
	dependencies := successfulInternalCompactionDependencies(t, []byte("not-json"))
	dependencies.priceRequest = func(_ *gin.Context, info *relaycommon.RelayInfo, _ int, _ *types.TokenCountMeta) (hosttypes.PriceData, error) {
		price := hosttypes.PriceData{QuotaToPreConsume: 25, ChannelRatio: 1, ChannelRatioSet: true}
		info.PriceData = price
		return price, nil
	}
	dependencies.preconsume = func(_ *gin.Context, _ int, info *relaycommon.RelayInfo) (internalCompactionBillingSession, *types.NewAPIError) {
		info.Billing = billing
		return billing, nil
	}
	dependencies.postConsume = func(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.Usage) (service.TextConsumeResult, error) {
		postConsumeCalls++
		billing.settled = true
		billing.needsRefund = false
		return service.TextConsumeResult{ActualQuota: 18, LogRecorded: true}, nil
	}
	dependencies.recordError = func(_ *gin.Context, _ *relaycommon.RelayInfo, resultCode string, other map[string]interface{}) error {
		recordErrorCalls++
		assert.Equal(t, "billable_execution_failed", resultCode)
		assert.Equal(t, true, other["billable_execution_failure"])
		return nil
	}

	executor, err := newInternalCompactionExecutor(request, dependencies)
	require.NoError(t, err)
	result, err := executor.Execute(context.Background())
	require.Error(t, err)
	assert.True(t, result.BillableExecutionFailure)
	assert.Equal(t, contextconsensus.CompactionChildStateLogged, result.State)
	assert.Equal(t, 18, result.SettledQuota)
	assert.False(t, result.Refunded)
	assert.Equal(t, 1, postConsumeCalls)
	assert.Equal(t, 0, billing.refundCalls)
	assert.Equal(t, 1, recordErrorCalls)
}

func TestInternalCompactionExecutorRejectsUnrecordedBillableFailure(t *testing.T) {
	parent, _ := newInternalCompactionParent(t)
	request, _ := validInternalCompactionRequest(t, parent)
	dependencies := successfulInternalCompactionDependencies(t, []byte("not-json"))
	dependencies.postConsume = func(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.Usage) (service.TextConsumeResult, error) {
		return service.TextConsumeResult{ActualQuota: 18, LogRecorded: false}, nil
	}
	recordErrorCalls := 0
	dependencies.recordError = func(_ *gin.Context, _ *relaycommon.RelayInfo, _ string, _ map[string]interface{}) error {
		recordErrorCalls++
		return nil
	}

	executor, err := newInternalCompactionExecutor(request, dependencies)
	require.NoError(t, err)
	result, err := executor.Execute(context.Background())
	require.ErrorContains(t, err, "consume log was not recorded")
	assert.Equal(t, contextconsensus.CompactionChildStateAuditFailed, result.State)
	assert.Equal(t, "billable_execution_audit_failed", result.ResultCode)
	assert.False(t, result.AuditRecorded)
	assert.Zero(t, recordErrorCalls)
}

func TestInternalCompactionExecutorRefundsOrdinaryExecutionFailure(t *testing.T) {
	parent, _ := newInternalCompactionParent(t)
	request, _ := validInternalCompactionRequest(t, parent)
	billing := &fakeInternalCompactionBilling{needsRefund: true, preConsumedQuota: 25}
	dependencies := successfulInternalCompactionDependencies(t, nil)
	dependencies.priceRequest = func(_ *gin.Context, info *relaycommon.RelayInfo, _ int, _ *types.TokenCountMeta) (hosttypes.PriceData, error) {
		price := hosttypes.PriceData{QuotaToPreConsume: 25, ChannelRatio: 1, ChannelRatioSet: true}
		info.PriceData = price
		return price, nil
	}
	dependencies.preconsume = func(_ *gin.Context, _ int, info *relaycommon.RelayInfo) (internalCompactionBillingSession, *types.NewAPIError) {
		info.Billing = billing
		return billing, nil
	}
	dependencies.executeAttempt = func(_ *gin.Context, _ *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
		return nil, types.NewError(errors.New("upstream unavailable"), types.ErrorCodeDoRequestFailed)
	}

	executor, err := newInternalCompactionExecutor(request, dependencies)
	require.NoError(t, err)
	result, err := executor.Execute(context.Background())
	require.Error(t, err)
	assert.Equal(t, contextconsensus.CompactionChildStateRefunded, result.State)
	assert.True(t, result.Refunded)
	assert.False(t, result.BillableExecutionFailure)
	assert.Equal(t, 1, billing.refundCalls)
}

func TestInternalCompactionExecutorRefundsZeroUsageAndRecordsFailureAudit(t *testing.T) {
	parent, _ := newInternalCompactionParent(t)
	request, summaryBody := validInternalCompactionRequest(t, parent)
	billing := &fakeInternalCompactionBilling{needsRefund: true, preConsumedQuota: 25}
	recordErrorCalls := 0
	postConsumeCalls := 0
	dependencies := successfulInternalCompactionDependencies(t, summaryBody)
	dependencies.priceRequest = func(_ *gin.Context, info *relaycommon.RelayInfo, _ int, _ *types.TokenCountMeta) (hosttypes.PriceData, error) {
		price := hosttypes.PriceData{QuotaToPreConsume: 25, ChannelRatio: 1, ChannelRatioSet: true}
		info.PriceData = price
		return price, nil
	}
	dependencies.preconsume = func(_ *gin.Context, _ int, info *relaycommon.RelayInfo) (internalCompactionBillingSession, *types.NewAPIError) {
		info.Billing = billing
		return billing, nil
	}
	dependencies.executeAttempt = func(ctx *gin.Context, _ *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
		_, err := ctx.Writer.Write(openAICompactionResponse(t, summaryBody))
		require.NoError(t, err)
		return &dto.Usage{}, nil
	}
	dependencies.postConsume = func(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.Usage) (service.TextConsumeResult, error) {
		postConsumeCalls++
		return service.TextConsumeResult{}, nil
	}
	dependencies.recordError = func(_ *gin.Context, _ *relaycommon.RelayInfo, resultCode string, _ map[string]interface{}) error {
		recordErrorCalls++
		assert.Equal(t, "execute_failed", resultCode)
		return nil
	}

	executor, err := newInternalCompactionExecutor(request, dependencies)
	require.NoError(t, err)
	result, err := executor.Execute(context.Background())
	require.ErrorContains(t, err, "did not include billable usage")
	assert.Equal(t, contextconsensus.CompactionChildStateRefunded, result.State)
	assert.True(t, result.Refunded)
	assert.True(t, result.AuditRecorded)
	assert.Equal(t, 1, billing.refundCalls)
	assert.Zero(t, postConsumeCalls)
	assert.Equal(t, 1, recordErrorCalls)
}

func TestInternalCompactionExecutorDoesNotExecuteAfterPreconsumeFailure(t *testing.T) {
	parent, _ := newInternalCompactionParent(t)
	request, _ := validInternalCompactionRequest(t, parent)
	executeCalls := 0
	dependencies := successfulInternalCompactionDependencies(t, nil)
	dependencies.priceRequest = func(_ *gin.Context, info *relaycommon.RelayInfo, _ int, _ *types.TokenCountMeta) (hosttypes.PriceData, error) {
		price := hosttypes.PriceData{QuotaToPreConsume: 25, ChannelRatio: 1, ChannelRatioSet: true}
		info.PriceData = price
		return price, nil
	}
	dependencies.preconsume = func(_ *gin.Context, _ int, _ *relaycommon.RelayInfo) (internalCompactionBillingSession, *types.NewAPIError) {
		return nil, types.NewError(errors.New("quota unavailable"), types.ErrorCodePreConsumeTokenQuotaFailed)
	}
	dependencies.executeAttempt = func(_ *gin.Context, _ *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
		executeCalls++
		return nil, nil
	}

	executor, err := newInternalCompactionExecutor(request, dependencies)
	require.NoError(t, err)
	result, err := executor.Execute(context.Background())
	require.ErrorContains(t, err, "quota unavailable")
	assert.Equal(t, "preconsume_failed", result.ResultCode)
	assert.Zero(t, executeCalls)
	assert.False(t, result.Refunded)
	assert.True(t, result.AuditRecorded)
}

func TestInternalCompactionExecutorRejectsEstimatedQuotaBeforePreconsume(t *testing.T) {
	parent, _ := newInternalCompactionParent(t)
	request, _ := validInternalCompactionRequest(t, parent)
	request.MaxQuota = 24
	preconsumeCalls := 0
	executeCalls := 0
	dependencies := successfulInternalCompactionDependencies(t, nil)
	dependencies.priceRequest = func(_ *gin.Context, info *relaycommon.RelayInfo, _ int, _ *types.TokenCountMeta) (hosttypes.PriceData, error) {
		price := hosttypes.PriceData{QuotaToPreConsume: 25, ChannelRatio: 1, ChannelRatioSet: true}
		info.PriceData = price
		return price, nil
	}
	dependencies.preconsume = func(_ *gin.Context, _ int, _ *relaycommon.RelayInfo) (internalCompactionBillingSession, *types.NewAPIError) {
		preconsumeCalls++
		return nil, nil
	}
	dependencies.executeAttempt = func(_ *gin.Context, _ *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
		executeCalls++
		return nil, nil
	}

	executor, err := newInternalCompactionExecutor(request, dependencies)
	require.NoError(t, err)
	result, err := executor.Execute(context.Background())
	require.ErrorContains(t, err, "exceeds configured maximum")
	assert.Equal(t, "prepare_failed", result.ResultCode)
	assert.Zero(t, preconsumeCalls)
	assert.Zero(t, executeCalls)
	assert.True(t, result.AuditRecorded)
}

func TestInternalCompactionExecutorReportsConsumeLogFailureWithoutRefund(t *testing.T) {
	parent, _ := newInternalCompactionParent(t)
	request, summaryBody := validInternalCompactionRequest(t, parent)
	billing := &fakeInternalCompactionBilling{needsRefund: true, preConsumedQuota: 25}
	logError := errors.New("consume log unavailable")
	dependencies := successfulInternalCompactionDependencies(t, summaryBody)
	dependencies.priceRequest = func(_ *gin.Context, info *relaycommon.RelayInfo, _ int, _ *types.TokenCountMeta) (hosttypes.PriceData, error) {
		price := hosttypes.PriceData{QuotaToPreConsume: 25, ChannelRatio: 1, ChannelRatioSet: true}
		info.PriceData = price
		return price, nil
	}
	dependencies.preconsume = func(_ *gin.Context, _ int, info *relaycommon.RelayInfo) (internalCompactionBillingSession, *types.NewAPIError) {
		info.Billing = billing
		return billing, nil
	}
	dependencies.postConsume = func(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.Usage) (service.TextConsumeResult, error) {
		billing.settled = true
		billing.needsRefund = false
		return service.TextConsumeResult{ActualQuota: 18, SettlementError: nil, LogError: logError}, logError
	}

	executor, err := newInternalCompactionExecutor(request, dependencies)
	require.NoError(t, err)
	result, err := executor.Execute(nil)
	require.ErrorIs(t, err, logError)
	assert.Equal(t, contextconsensus.CompactionChildStateAuditFailed, result.State)
	assert.Equal(t, "audit_failed", result.ResultCode)
	assert.Equal(t, 18, result.SettledQuota)
	assert.False(t, result.Refunded)
	assert.Equal(t, 0, billing.refundCalls)
}

func TestInternalCompactionExecutorRejectsUnrecordedConsumeAudit(t *testing.T) {
	parent, _ := newInternalCompactionParent(t)
	request, summaryBody := validInternalCompactionRequest(t, parent)
	dependencies := successfulInternalCompactionDependencies(t, summaryBody)
	dependencies.postConsume = func(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.Usage) (service.TextConsumeResult, error) {
		return service.TextConsumeResult{ActualQuota: 0, LogRecorded: false}, nil
	}

	executor, err := newInternalCompactionExecutor(request, dependencies)
	require.NoError(t, err)
	result, err := executor.Execute(context.Background())
	require.ErrorContains(t, err, "consume log was not recorded")
	assert.Equal(t, contextconsensus.CompactionChildStateAuditFailed, result.State)
	assert.False(t, result.AuditRecorded)
}

func TestInternalCompactionExecutorAuditsUnauthorizedSelectedChannel(t *testing.T) {
	parent, _ := newInternalCompactionParent(t)
	request, _ := validInternalCompactionRequest(t, parent)
	dependencies := successfulInternalCompactionDependencies(t, nil)
	dependencies.selectChannel = func(_ *gin.Context, _, _ string) (*model.Channel, string, error) {
		return &model.Channel{Id: 10, Name: "unauthorized", Type: 1, Key: "other-secret"}, "default", nil
	}
	recordErrorCalls := 0
	dependencies.recordError = func(ctx *gin.Context, info *relaycommon.RelayInfo, resultCode string, other map[string]interface{}) error {
		recordErrorCalls++
		assert.Equal(t, "prepare_failed", resultCode)
		assert.Equal(t, "child-request", common.GetContextKeyString(ctx, common.RequestIdKey))
		assert.Equal(t, "summary-model", info.OriginModelName)
		assert.Equal(t, "parent-request", other["parent_request_id"])
		return nil
	}

	executor, err := newInternalCompactionExecutor(request, dependencies)
	require.NoError(t, err)
	result, err := executor.Execute(context.Background())
	require.ErrorContains(t, err, "outside the frozen authorization set")
	assert.Equal(t, 1, recordErrorCalls)
	assert.True(t, result.AuditRecorded)
}

func TestInternalCompactionExecutorRequiresPositiveQuotaCap(t *testing.T) {
	parent, _ := newInternalCompactionParent(t)
	request, _ := validInternalCompactionRequest(t, parent)
	request.MaxQuota = 0

	executor, err := newInternalCompactionExecutor(request, successfulInternalCompactionDependencies(t, nil))
	require.ErrorContains(t, err, "max quota must be positive")
	assert.Nil(t, executor)
}

func TestInternalCompactionExecutorRequiresPositiveInputCap(t *testing.T) {
	parent, _ := newInternalCompactionParent(t)
	request, _ := validInternalCompactionRequest(t, parent)
	request.MaxInputTokens = 0

	executor, err := newInternalCompactionExecutor(request, successfulInternalCompactionDependencies(t, nil))
	require.ErrorContains(t, err, "max input tokens must be positive")
	assert.Nil(t, executor)
}

func TestInternalCompactionExecutorRejectsInputAboveFrozenCap(t *testing.T) {
	parent, _ := newInternalCompactionParent(t)
	request, summaryBody := validInternalCompactionRequest(t, parent)
	request.MaxInputTokens = 19

	executor, err := newInternalCompactionExecutor(request, successfulInternalCompactionDependencies(t, summaryBody))
	require.NoError(t, err)
	_, err = executor.Execute(context.Background())
	require.ErrorContains(t, err, "compaction input exceeds configured maximum")
}

func TestInternalCompactionExecutorRejectsModelOutsideFrozenPool(t *testing.T) {
	parent, _ := newInternalCompactionParent(t)
	request, _ := validInternalCompactionRequest(t, parent)
	request.ModelPool = []string{"another-model"}

	executor, err := newInternalCompactionExecutor(request, successfulInternalCompactionDependencies(t, nil))
	require.Error(t, err)
	assert.Nil(t, executor)
}

func TestBoundedInternalResponseWriterRejectsOverflow(t *testing.T) {
	writer := newBoundedInternalResponseWriter(4)
	written, err := writer.Write([]byte("12345"))
	require.ErrorIs(t, err, errInternalResponseTooLarge)
	assert.Zero(t, written)
	assert.True(t, writer.Overflowed())
	assert.Empty(t, writer.Bytes())
}

type fakeInternalCompactionBilling struct {
	needsRefund      bool
	settled          bool
	refundCalls      int
	preConsumedQuota int
}

func (billing *fakeInternalCompactionBilling) Settle(_ int) error {
	billing.settled = true
	billing.needsRefund = false
	return nil
}

func (billing *fakeInternalCompactionBilling) Refund(*gin.Context) {
	_ = billing.RefundSync(nil)
}

func (billing *fakeInternalCompactionBilling) NeedsRefund() bool {
	return billing.needsRefund
}

func (billing *fakeInternalCompactionBilling) GetPreConsumedQuota() int {
	return billing.preConsumedQuota
}

func (billing *fakeInternalCompactionBilling) Reserve(_ int) error {
	return nil
}

func (billing *fakeInternalCompactionBilling) RefundSync(*gin.Context) error {
	billing.refundCalls++
	billing.needsRefund = false
	return nil
}

func newInternalCompactionParent(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(ctx, common.RequestIdKey, "parent-request")
	common.SetContextKey(ctx, constant.ContextKeyUserId, 7)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyUserQuota, 1000)
	common.SetContextKey(ctx, constant.ContextKeyUserEmail, "user@example.test")
	common.SetContextKey(ctx, constant.ContextKeyUserName, "test-user")
	common.SetContextKey(ctx, constant.ContextKeyUserSetting, dto.UserSetting{})
	common.SetContextKey(ctx, constant.ContextKeyTokenId, 11)
	common.SetContextKey(ctx, constant.ContextKeyTokenKey, "parent-token-secret")
	common.SetContextKey(ctx, constant.ContextKeyTokenUnlimited, false)
	common.SetContextKey(ctx, constant.ContextKeyTokenGroup, "default")
	ctx.Set("token_name", "test-token")
	ctx.Set("token_quota", 1000)
	return ctx, recorder
}

func validInternalCompactionRequest(t *testing.T, parent *gin.Context) (InternalCompactionExecutorRequest, []byte) {
	t.Helper()
	coveredRange := contextconsensus.SummarySourceRange{StartSequence: 0, EndSequence: 1, SourceDigest: "covered-digest"}
	plan := contextconsensus.CompactionPlan{
		SourceDigest:     "source-digest",
		CoveredRanges:    []contextconsensus.SummarySourceRange{coveredRange},
		MaxSummaryTokens: 128,
		PolicyVersion:    "policy-v1",
	}
	summary := contextconsensus.ConsensusSummary{
		Version:             contextconsensus.ConsensusSummaryVersion,
		TaskGoal:            []contextconsensus.ConsensusFact{},
		CurrentPhase:        "continue implementation",
		Decisions:           []contextconsensus.ConsensusFact{},
		MustPreserve:        []contextconsensus.ConsensusFact{},
		OpenQuestions:       []contextconsensus.ConsensusFact{},
		UserPreferences:     []contextconsensus.ConsensusFact{},
		DomainTerms:         map[string]contextconsensus.ConsensusFact{},
		CompletedSteps:      []contextconsensus.ConsensusFact{},
		PendingSteps:        []contextconsensus.ConsensusFact{},
		ArtifactRefs:        []contextconsensus.ConsensusFact{},
		ToolResultSummaries: []contextconsensus.ConsensusFact{},
		SourceRanges:        []contextconsensus.SummarySourceRange{coveredRange},
		SourceDigest:        "source-digest",
	}
	summaryBody, err := common.Marshal(summary)
	require.NoError(t, err)
	return InternalCompactionExecutorRequest{
		ParentContext:     parent,
		Model:             "summary-model",
		ModelPool:         []string{"summary-model"},
		AllowedChannelIDs: []int{9},
		PolicyVersion:     "policy-v1",
		SourceDigest:      "source-digest",
		MaxOutputTokens:   128,
		MaxInputTokens:    128000,
		SummaryRequest: &dto.GeneralOpenAIRequest{Messages: []dto.Message{
			{Role: "system", Content: "Return the required JSON summary."},
			{Role: "user", Content: "Summarize the frozen source range."},
		}},
		Plan:             plan,
		MaxQuota:         100,
		MaxResponseBytes: 4096,
	}, summaryBody
}

func successfulInternalCompactionDependencies(t *testing.T, summaryBody []byte) internalCompactionDependencies {
	t.Helper()
	return internalCompactionDependencies{
		newRequestId: func() string { return "child-request" },
		selectChannel: func(_ *gin.Context, _, _ string) (*model.Channel, string, error) {
			return &model.Channel{Id: 9, Name: "summary", Type: 1, Key: "child-channel-secret"}, "default", nil
		},
		setupChannel: func(ctx *gin.Context, channel *model.Channel, modelName string) *types.NewAPIError {
			common.SetContextKey(ctx, constant.ContextKeyChannelId, channel.Id)
			common.SetContextKey(ctx, constant.ContextKeyChannelName, channel.Name)
			common.SetContextKey(ctx, constant.ContextKeyChannelType, channel.Type)
			common.SetContextKey(ctx, constant.ContextKeyChannelKey, channel.Key)
			common.SetContextKey(ctx, constant.ContextKeyChannelPriceRatio, float64(1))
			common.SetContextKey(ctx, constant.ContextKeyOriginalModel, modelName)
			return nil
		},
		estimateTokens: func(_ *gin.Context, _ *types.TokenCountMeta, _ *relaycommon.RelayInfo) (int, error) {
			return 20, nil
		},
		priceRequest: func(_ *gin.Context, info *relaycommon.RelayInfo, _ int, _ *types.TokenCountMeta) (hosttypes.PriceData, error) {
			price := hosttypes.PriceData{FreeModel: true, ChannelRatio: 1, ChannelRatioSet: true}
			info.PriceData = price
			return price, nil
		},
		preconsume: func(_ *gin.Context, _ int, _ *relaycommon.RelayInfo) (internalCompactionBillingSession, *types.NewAPIError) {
			t.Fatal("free model must not pre-consume quota")
			return nil, nil
		},
		executeAttempt: func(ctx *gin.Context, _ *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
			_, err := ctx.Writer.Write(openAICompactionResponse(t, summaryBody))
			require.NoError(t, err)
			return &dto.Usage{PromptTokens: 30, CompletionTokens: 12}, nil
		},
		postConsume: func(_ *gin.Context, _ *relaycommon.RelayInfo, _ *dto.Usage) (service.TextConsumeResult, error) {
			return service.TextConsumeResult{ActualQuota: 0, LogRecorded: true}, nil
		},
		recordError: func(_ *gin.Context, _ *relaycommon.RelayInfo, _ string, _ map[string]interface{}) error {
			return nil
		},
	}
}

func openAICompactionResponse(t *testing.T, summaryBody []byte) []byte {
	t.Helper()
	body, err := common.Marshal(dto.OpenAITextResponse{
		Choices: []dto.OpenAITextResponseChoice{{
			Index:   0,
			Message: dto.Message{Role: "assistant", Content: string(summaryBody)},
		}},
	})
	require.NoError(t, err)
	return body
}
