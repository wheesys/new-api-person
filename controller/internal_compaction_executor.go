package controller

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

const (
	defaultInternalCompactionTimeout       = 60 * time.Second
	defaultInternalCompactionResponseBytes = int64(2 << 20)
	internalCompactionRequestPath          = "/v1/chat/completions"
)

type InternalCompactionExecutorRequest struct {
	ParentContext          *gin.Context
	Model                  string
	ModelPool              []string
	AllowedChannelIDs      []int
	PolicyVersion          string
	SourceDigest           string
	SummaryVersion         int
	MaxOutputTokens        int
	MaxInputTokens         int
	SummaryRequest         *dto.GeneralOpenAIRequest
	Plan                   contextconsensus.CompactionPlan
	ManagedRevisionPlan    *contextconsensus.ManagedRevisionPlan
	BillingOperationSeed   *managedBillingOperationSeed
	MaxQuota               int
	Timeout                time.Duration
	MaxResponseBytes       int64
	ManagedOutcome         *contextconsensus.ManagedOutcomeSession
	ManagedSummarySnapshot *contextconsensus.ManagedSummaryExecutionSnapshot
}

type InternalCompactionExecutor struct {
	executor  *contextconsensus.CompactionChildExecutor
	request   contextconsensus.CompactionChildRequest
	lifecycle *internalCompactionLifecycle
}

type internalCompactionIdentity struct {
	userId                 int
	userGroup              string
	usingGroup             string
	userQuota              int
	userEmail              string
	userName               string
	userSetting            dto.UserSetting
	tokenId                int
	tokenKey               string
	tokenName              string
	tokenQuota             int
	tokenUnlimited         bool
	tokenGroup             string
	tokenModelLimitEnabled bool
	tokenModelLimit        map[string]bool
}

type internalCompactionBillingSession interface {
	NeedsRefund() bool
	RefundSync(*gin.Context) error
}

type internalCompactionDependencies struct {
	newRequestId   func() string
	selectChannel  func(*gin.Context, string, string) (*model.Channel, string, error)
	setupChannel   func(*gin.Context, *model.Channel, string) *types.NewAPIError
	estimateTokens func(*gin.Context, *types.TokenCountMeta, *relaycommon.RelayInfo) (int, error)
	priceRequest   func(*gin.Context, *relaycommon.RelayInfo, int, *types.TokenCountMeta) (hosttypes.PriceData, error)
	preconsume     func(*gin.Context, int, *relaycommon.RelayInfo) (internalCompactionBillingSession, *types.NewAPIError)
	executeAttempt func(*gin.Context, *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError)
	postConsume    func(*gin.Context, *relaycommon.RelayInfo, *dto.Usage) (service.TextConsumeResult, error)
	recordError    func(*gin.Context, *relaycommon.RelayInfo, string, map[string]interface{}) error
}

type internalCompactionLifecycle struct {
	identity               internalCompactionIdentity
	modelPool              map[string]struct{}
	allowedChannelIDs      map[int]struct{}
	summaryRequest         *dto.GeneralOpenAIRequest
	plan                   contextconsensus.CompactionPlan
	managedRevisionPlan    *contextconsensus.ManagedRevisionPlan
	billingOperationSeed   *managedBillingOperationSeed
	maxQuota               int
	maxInputTokens         int
	timeout                time.Duration
	maxResponseBytes       int64
	managedOutcome         *contextconsensus.ManagedOutcomeSession
	managedSummarySnapshot *contextconsensus.ManagedSummaryExecutionSnapshot
	dependencies           internalCompactionDependencies

	mutex                sync.Mutex
	runtime              *internalCompactionRuntime
	billingSession       internalCompactionBillingSession
	postConsumeAttempted bool
	postConsumeResult    service.TextConsumeResult
}

type internalCompactionRuntime struct {
	context       *gin.Context
	relayInfo     *relaycommon.RelayInfo
	request       *dto.GeneralOpenAIRequest
	response      *boundedInternalResponseWriter
	cancel        context.CancelFunc
	usage         *dto.Usage
	selectedGroup string
}

type internalCompactionRequestIdGenerator struct {
	newRequestId func() string
}

func NewInternalCompactionExecutor(request InternalCompactionExecutorRequest) (*InternalCompactionExecutor, error) {
	return newInternalCompactionExecutor(request, defaultInternalCompactionDependencies())
}

func newInternalCompactionExecutor(request InternalCompactionExecutorRequest, dependencies internalCompactionDependencies) (*InternalCompactionExecutor, error) {
	if request.ParentContext == nil {
		return nil, fmt.Errorf("parent context is required")
	}
	if request.SummaryRequest == nil {
		return nil, fmt.Errorf("summary request is required")
	}
	if request.MaxQuota <= 0 {
		return nil, fmt.Errorf("compaction max quota must be positive")
	}
	if request.MaxInputTokens <= 0 {
		return nil, fmt.Errorf("compaction max input tokens must be positive")
	}
	if request.PolicyVersion != request.Plan.PolicyVersion || request.SourceDigest != request.Plan.SourceDigest ||
		request.SummaryVersion != request.Plan.SummaryVersion || request.MaxOutputTokens != request.Plan.MaxSummaryTokens {
		return nil, fmt.Errorf("compaction request does not match its frozen plan")
	}
	parentRequestId := common.GetContextKeyString(request.ParentContext, common.RequestIdKey)
	if strings.TrimSpace(parentRequestId) == "" {
		return nil, fmt.Errorf("parent request ID is required")
	}
	summaryRequest, err := normalizeInternalCompactionRequest(request.SummaryRequest, request.Model, request.MaxOutputTokens)
	if err != nil {
		return nil, err
	}
	modelPool, err := freezeInternalCompactionModelPool(request.ModelPool)
	if err != nil {
		return nil, err
	}
	if _, ok := modelPool[ratio_setting.FormatMatchingModelName(request.Model)]; !ok {
		return nil, fmt.Errorf("compaction model is not in the frozen model pool")
	}
	allowedChannelIDs, err := freezeInternalCompactionChannelIDs(request.AllowedChannelIDs)
	if err != nil {
		return nil, err
	}
	identity := captureInternalCompactionIdentity(request.ParentContext)
	if identity.tokenModelLimitEnabled && !identity.tokenModelLimit[ratio_setting.FormatMatchingModelName(request.Model)] {
		return nil, fmt.Errorf("token has no access to the compaction model")
	}
	if request.Timeout <= 0 {
		request.Timeout = defaultInternalCompactionTimeout
	}
	if request.MaxResponseBytes <= 0 {
		request.MaxResponseBytes = defaultInternalCompactionResponseBytes
	}
	lifecycle := &internalCompactionLifecycle{
		identity:               identity,
		modelPool:              modelPool,
		allowedChannelIDs:      allowedChannelIDs,
		summaryRequest:         summaryRequest,
		plan:                   request.Plan,
		managedRevisionPlan:    request.ManagedRevisionPlan,
		billingOperationSeed:   request.BillingOperationSeed,
		maxQuota:               request.MaxQuota,
		maxInputTokens:         request.MaxInputTokens,
		timeout:                request.Timeout,
		maxResponseBytes:       request.MaxResponseBytes,
		managedOutcome:         request.ManagedOutcome,
		managedSummarySnapshot: request.ManagedSummarySnapshot,
		dependencies:           dependencies,
	}
	executor, err := contextconsensus.NewCompactionChildExecutor(contextconsensus.CompactionChildDependencies{
		RequestIDGenerator: internalCompactionRequestIdGenerator{newRequestId: dependencies.newRequestId},
		Preparer:           lifecycle,
		Billing:            lifecycle,
		Runner:             lifecycle,
		Auditor:            lifecycle,
	})
	if err != nil {
		return nil, err
	}
	return &InternalCompactionExecutor{
		executor:  executor,
		lifecycle: lifecycle,
		request: contextconsensus.CompactionChildRequest{
			ParentRequestID: parentRequestId,
			Model:           strings.TrimSpace(request.Model),
			PolicyVersion:   request.PolicyVersion,
			SourceDigest:    request.SourceDigest,
			MaxOutputTokens: request.MaxOutputTokens,
		},
	}, nil
}

func (executor *InternalCompactionExecutor) SelectedChannelID() int {
	if executor == nil || executor.lifecycle == nil {
		return 0
	}
	runtime, err := executor.lifecycle.getRuntime()
	if err != nil || runtime.relayInfo == nil {
		return 0
	}
	return runtime.relayInfo.ChannelId
}

func (executor *InternalCompactionExecutor) Execute(ctx context.Context) (contextconsensus.CompactionChildResult, error) {
	if executor == nil || executor.executor == nil {
		return contextconsensus.CompactionChildResult{}, fmt.Errorf("internal compaction executor is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return executor.executor.Execute(ctx, executor.request)
}

func defaultInternalCompactionDependencies() internalCompactionDependencies {
	return internalCompactionDependencies{
		newRequestId: common.NewRequestId,
		selectChannel: func(ctx *gin.Context, modelName, tokenGroup string) (*model.Channel, string, error) {
			retry := 0
			return service.CacheGetRandomSatisfiedChannel(&service.RetryParam{
				Ctx: ctx, TokenGroup: tokenGroup, ModelName: modelName, RequestPath: internalCompactionRequestPath, Retry: &retry,
			})
		},
		setupChannel:   middleware.SetupContextForSelectedChannel,
		estimateTokens: service.EstimateRequestToken,
		priceRequest:   helper.ModelPriceHelper,
		preconsume: func(ctx *gin.Context, quota int, info *relaycommon.RelayInfo) (internalCompactionBillingSession, *types.NewAPIError) {
			if apiError := service.PreConsumeBilling(ctx, quota, info); apiError != nil {
				return nil, apiError
			}
			session, ok := info.Billing.(*service.BillingSession)
			if !ok {
				return nil, types.NewError(errors.New("internal compaction billing session is unavailable"), types.ErrorCodeUpdateDataError)
			}
			return session, nil
		},
		executeAttempt: relay.ExecuteTextAttemptWithoutQuota,
		postConsume: func(ctx *gin.Context, info *relaycommon.RelayInfo, usage *dto.Usage) (service.TextConsumeResult, error) {
			return service.PostTextConsumeQuotaResult(ctx, info, usage, nil)
		},
		recordError: recordInternalCompactionError,
	}
}

func (generator internalCompactionRequestIdGenerator) NewChildRequestID(parentRequestID string) (string, error) {
	if generator.newRequestId == nil {
		return "", fmt.Errorf("request ID generator is unavailable")
	}
	return generator.newRequestId(), nil
}

func (lifecycle *internalCompactionLifecycle) PrepareCompactionChild(ctx context.Context, descriptor contextconsensus.CompactionChildDescriptor) (contextconsensus.PreparedCompactionChild, error) {
	if _, ok := lifecycle.modelPool[ratio_setting.FormatMatchingModelName(descriptor.Model)]; !ok {
		return contextconsensus.PreparedCompactionChild{}, fmt.Errorf("compaction model is outside the frozen model pool")
	}
	childRequest, err := common.DeepCopy(lifecycle.summaryRequest)
	if err != nil {
		return contextconsensus.PreparedCompactionChild{}, fmt.Errorf("copy summary request: %w", err)
	}
	childContext, responseWriter, cancel, err := lifecycle.newChildContext(ctx, descriptor, childRequest)
	if err != nil {
		return contextconsensus.PreparedCompactionChild{}, err
	}
	runtime := &internalCompactionRuntime{
		context: childContext, request: childRequest, response: responseWriter, cancel: cancel,
	}
	lifecycle.mutex.Lock()
	lifecycle.runtime = runtime
	lifecycle.mutex.Unlock()

	selectedChannel, selectedGroup, err := lifecycle.dependencies.selectChannel(childContext, descriptor.Model, lifecycle.identity.tokenGroup)
	selectedOutsideAuthorization := false
	if err != nil || selectedChannel == nil {
		selectedChannel, selectedGroup, err = lifecycle.selectFrozenCompactionChannel(descriptor.Model)
	} else if _, allowed := lifecycle.allowedChannelIDs[selectedChannel.Id]; !allowed {
		selectedOutsideAuthorization = true
		selectedChannel, selectedGroup, err = lifecycle.selectFrozenCompactionChannel(descriptor.Model)
	}
	if err != nil {
		if selectedOutsideAuthorization {
			return contextconsensus.PreparedCompactionChild{}, fmt.Errorf("selected compaction channel is outside the frozen authorization set")
		}
		return contextconsensus.PreparedCompactionChild{}, fmt.Errorf("select compaction channel: %w", err)
	}
	if selectedChannel == nil {
		return contextconsensus.PreparedCompactionChild{}, fmt.Errorf("no authorized compaction channel is available")
	}
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || selectedChannel.GetSetting().PassThroughBodyEnabled {
		return contextconsensus.PreparedCompactionChild{}, fmt.Errorf("pass-through channels are not allowed for internal compaction")
	}
	if apiError := lifecycle.dependencies.setupChannel(childContext, selectedChannel, descriptor.Model); apiError != nil {
		return contextconsensus.PreparedCompactionChild{}, fmt.Errorf("setup compaction channel: %w", apiError)
	}
	if selectedGroup != "" {
		common.SetContextKey(childContext, constant.ContextKeyUsingGroup, selectedGroup)
	}
	relayInfo := relaycommon.GenRelayInfoOpenAI(childContext, childRequest)
	relayInfo.RequestPurpose = contextconsensus.CompactionRequestPurposeContextCompaction
	relayInfo.ParentRequestId = descriptor.ParentRequestID
	relayInfo.PolicyVersion = descriptor.PolicyVersion
	billingInput, err := helper.BuildBillingExprRequestInputFromRequest(childRequest, relayInfo.RequestHeaders)
	if err != nil {
		return contextconsensus.PreparedCompactionChild{}, fmt.Errorf("build compaction billing input: %w", err)
	}
	relayInfo.BillingRequestInput = &billingInput
	lifecycle.mutex.Lock()
	runtime.relayInfo = relayInfo
	runtime.selectedGroup = selectedGroup
	lifecycle.mutex.Unlock()
	meta := childRequest.GetTokenCountMeta()
	promptTokens, err := lifecycle.dependencies.estimateTokens(childContext, meta, relayInfo)
	if err != nil {
		return contextconsensus.PreparedCompactionChild{}, fmt.Errorf("estimate compaction tokens: %w", err)
	}
	if promptTokens > lifecycle.maxInputTokens {
		return contextconsensus.PreparedCompactionChild{}, fmt.Errorf("compaction input exceeds configured maximum")
	}
	relayInfo.SetEstimatePromptTokens(promptTokens)
	priceData, err := lifecycle.dependencies.priceRequest(childContext, relayInfo, promptTokens, meta)
	if err != nil {
		return contextconsensus.PreparedCompactionChild{}, fmt.Errorf("price compaction request: %w", err)
	}
	if priceData.QuotaToPreConsume > lifecycle.maxQuota {
		return contextconsensus.PreparedCompactionChild{}, fmt.Errorf("compaction pre-consume quota exceeds configured maximum")
	}
	relayInfo.PriceData = priceData
	if lifecycle.billingOperationSeed != nil {
		relayInfo.BillingOperation, err = buildManagedBillingOperationIdentity(*lifecycle.billingOperationSeed, relayInfo)
		if err != nil {
			return contextconsensus.PreparedCompactionChild{}, err
		}
	}
	preparedBody, err := common.Marshal(childRequest)
	if err != nil {
		return contextconsensus.PreparedCompactionChild{}, fmt.Errorf("marshal prepared compaction request: %w", err)
	}
	return contextconsensus.PreparedCompactionChild{
		PreparationID:         descriptor.ChildRequestID,
		PreparedRequestDigest: hex.EncodeToString(common.Sha256Raw(preparedBody)),
	}, nil
}

func (lifecycle *internalCompactionLifecycle) selectFrozenCompactionChannel(modelName string) (*model.Channel, string, error) {
	if model.DB == nil {
		return nil, "", fmt.Errorf("channel repository is unavailable")
	}
	channelIDs := make([]int, 0, len(lifecycle.allowedChannelIDs))
	for channelID := range lifecycle.allowedChannelIDs {
		channelIDs = append(channelIDs, channelID)
	}
	sort.Ints(channelIDs)
	groups := []string{lifecycle.identity.tokenGroup}
	if lifecycle.identity.tokenGroup == "auto" {
		groups = service.GetUserAutoGroup(lifecycle.identity.userGroup)
	}
	for _, channelID := range channelIDs {
		channel, err := model.GetChannelById(channelID, true)
		if err != nil || channel == nil || channel.Status != common.ChannelStatusEnabled {
			continue
		}
		for _, group := range groups {
			if group != "" && model.IsChannelEnabledForGroupModel(group, modelName, channelID) {
				return channel, group, nil
			}
		}
	}
	return nil, "", fmt.Errorf("no enabled channel in the frozen compaction authorization set supports model %s", modelName)
}

func (lifecycle *internalCompactionLifecycle) PreconsumeCompactionChild(_ context.Context, descriptor contextconsensus.CompactionChildDescriptor, _ contextconsensus.PreparedCompactionChild) (*contextconsensus.CompactionBillingReceipt, error) {
	runtime, err := lifecycle.getRuntime()
	if err != nil {
		return nil, err
	}
	if runtime.relayInfo.PriceData.FreeModel && runtime.relayInfo.BillingOperation == nil {
		return &contextconsensus.CompactionBillingReceipt{BillingReference: descriptor.ChildRequestID}, nil
	}
	session, apiError := lifecycle.dependencies.preconsume(runtime.context, runtime.relayInfo.PriceData.QuotaToPreConsume, runtime.relayInfo)
	if apiError != nil {
		return nil, fmt.Errorf("preconsume compaction quota: %w", apiError)
	}
	if session == nil {
		return nil, fmt.Errorf("compaction billing session is unavailable")
	}
	lifecycle.mutex.Lock()
	lifecycle.billingSession = session
	lifecycle.mutex.Unlock()
	return &contextconsensus.CompactionBillingReceipt{
		BillingReference: descriptor.ChildRequestID,
		ReservedQuota:    runtime.relayInfo.PriceData.QuotaToPreConsume,
	}, nil
}

func (lifecycle *internalCompactionLifecycle) NeedsRefund(_ *contextconsensus.CompactionBillingReceipt) bool {
	lifecycle.mutex.Lock()
	session := lifecycle.billingSession
	lifecycle.mutex.Unlock()
	return session != nil && session.NeedsRefund()
}

func (lifecycle *internalCompactionLifecycle) SettleCompactionChild(_ context.Context, _ *contextconsensus.CompactionBillingReceipt, output contextconsensus.CompactionExecutionOutput) (contextconsensus.CompactionSettlement, error) {
	runtime, err := lifecycle.getRuntime()
	if err != nil {
		return contextconsensus.CompactionSettlement{}, err
	}
	if runtime.usage == nil {
		return contextconsensus.CompactionSettlement{}, fmt.Errorf("compaction usage is unavailable")
	}
	if lifecycle.managedOutcome != nil && output.Summary.Version != 0 {
		if lifecycle.managedSummarySnapshot == nil {
			return contextconsensus.CompactionSettlement{}, fmt.Errorf("managed summary outcome snapshot is unavailable")
		}
		nextState, buildErr := contextconsensus.BuildNextManagedConsensusState(lifecycle.managedSummarySnapshot.Plan, output.Summary, time.Now())
		if buildErr != nil {
			return contextconsensus.CompactionSettlement{}, buildErr
		}
		checkpoint, checkpointErr := lifecycle.managedOutcome.SummaryCheckpoint(runtime.context.Request.Context(), output.Summary, nextState)
		if checkpointErr != nil {
			return contextconsensus.CompactionSettlement{}, checkpointErr
		}
		runtime.relayInfo.ManagedOutcomeCheckpoint = managedOutcomeRelayCheckpoint(checkpoint)
	}
	consumeResult, consumeError := lifecycle.dependencies.postConsume(runtime.context, runtime.relayInfo, runtime.usage)
	lifecycle.mutex.Lock()
	lifecycle.postConsumeAttempted = true
	lifecycle.postConsumeResult = consumeResult
	lifecycle.mutex.Unlock()
	if consumeResult.SettlementError != nil {
		return contextconsensus.CompactionSettlement{SettledQuota: consumeResult.ActualQuota}, consumeResult.SettlementError
	}
	if consumeError != nil && consumeResult.LogError == nil {
		return contextconsensus.CompactionSettlement{SettledQuota: consumeResult.ActualQuota}, consumeError
	}
	_ = output
	return contextconsensus.CompactionSettlement{SettledQuota: consumeResult.ActualQuota}, nil
}

func (lifecycle *internalCompactionLifecycle) RefundCompactionChild(_ context.Context, _ *contextconsensus.CompactionBillingReceipt) error {
	runtime, err := lifecycle.getRuntime()
	if err != nil {
		return err
	}
	lifecycle.mutex.Lock()
	session := lifecycle.billingSession
	lifecycle.mutex.Unlock()
	if session == nil {
		return nil
	}
	return session.RefundSync(runtime.context)
}

func (lifecycle *internalCompactionLifecycle) ExecuteCompactionChild(_ context.Context, _ contextconsensus.CompactionChildDescriptor, _ contextconsensus.PreparedCompactionChild) (contextconsensus.CompactionExecutionOutput, error) {
	runtime, err := lifecycle.getRuntime()
	if err != nil {
		return contextconsensus.CompactionExecutionOutput{}, err
	}
	if lifecycle.managedOutcome != nil {
		if err := lifecycle.managedOutcome.MarkSummaryDispatched(runtime.context.Request.Context()); err != nil {
			return contextconsensus.CompactionExecutionOutput{}, fmt.Errorf("persist managed summary dispatch: %w", err)
		}
	}
	usage, apiError := lifecycle.dependencies.executeAttempt(runtime.context, runtime.relayInfo)
	if apiError != nil {
		return contextconsensus.CompactionExecutionOutput{}, fmt.Errorf("execute compaction request: %w", apiError)
	}
	runtime.usage = usage
	compactionUsage := contextconsensus.CompactionUsage{}
	if usage != nil {
		compactionUsage.InputTokens = usage.PromptTokens
		compactionUsage.OutputTokens = usage.CompletionTokens
	}
	responseBody := append([]byte(nil), runtime.response.Bytes()...)
	output := contextconsensus.CompactionExecutionOutput{
		SummaryDigest: hex.EncodeToString(common.Sha256Raw(responseBody)),
		Usage:         compactionUsage,
	}
	if usage == nil || usage.PromptTokens+usage.CompletionTokens <= 0 {
		return output, fmt.Errorf("compaction upstream response did not include billable usage")
	}
	if runtime.response.Overflowed() {
		return output, contextconsensus.NewBillableCompactionExecutionError(output, errInternalResponseTooLarge)
	}
	var response dto.OpenAITextResponse
	if err := common.Unmarshal(responseBody, &response); err != nil {
		return output, contextconsensus.NewBillableCompactionExecutionError(output, fmt.Errorf("decode compaction response: %w", err))
	}
	if len(response.Choices) != 1 || response.Choices[0].Message.Role != "assistant" {
		return output, contextconsensus.NewBillableCompactionExecutionError(output, fmt.Errorf("compaction response must contain one assistant choice"))
	}
	summaryBody := []byte(strings.TrimSpace(response.Choices[0].Message.StringContent()))
	output.SummaryDigest = hex.EncodeToString(common.Sha256Raw(summaryBody))
	var summary contextconsensus.ConsensusSummary
	if lifecycle.managedRevisionPlan != nil {
		summary, err = contextconsensus.ParseAndValidateManagedRevisionSummary(summaryBody, *lifecycle.managedRevisionPlan)
	} else {
		switch lifecycle.plan.SummaryVersion {
		case contextconsensus.ConsensusSummaryVersion:
			summary, err = contextconsensus.ParseAndValidateConsensusSummaryV1(summaryBody, lifecycle.plan)
		case contextconsensus.ConsensusSummaryVersionV2:
			summary, err = contextconsensus.ParseAndValidateConsensusSummaryV2(summaryBody, lifecycle.plan)
		default:
			err = fmt.Errorf("unsupported consensus summary version %d", lifecycle.plan.SummaryVersion)
		}
	}
	if err != nil {
		return output, contextconsensus.NewBillableCompactionExecutionError(output, fmt.Errorf("validate compaction summary: %w", err))
	}
	if lifecycle.plan.SummaryVersion == contextconsensus.ConsensusSummaryVersionV2 {
		canonicalSummary, marshalErr := common.Marshal(summary)
		if marshalErr != nil {
			return output, contextconsensus.NewBillableCompactionExecutionError(output, fmt.Errorf("encode validated compaction summary: %w", marshalErr))
		}
		output.SummaryDigest = hex.EncodeToString(common.Sha256Raw(canonicalSummary))
	}
	output.Summary = summary
	return output, nil
}

func (lifecycle *internalCompactionLifecycle) RecordCompactionChild(_ context.Context, record contextconsensus.CompactionAuditRecord) error {
	lifecycle.mutex.Lock()
	runtime := lifecycle.runtime
	postConsumeAttempted := lifecycle.postConsumeAttempted
	consumeResult := lifecycle.postConsumeResult
	lifecycle.mutex.Unlock()
	if runtime == nil {
		return fmt.Errorf("internal compaction runtime was not created; audit was not recorded")
	}
	defer func() {
		common.CleanupBodyStorage(runtime.context)
		runtime.cancel()
	}()
	if consumeResult.LogError != nil {
		return consumeResult.LogError
	}
	if postConsumeAttempted && !consumeResult.LogRecorded {
		return fmt.Errorf("internal compaction consume log was not recorded")
	}
	if record.ResultCode == "success" {
		return nil
	}
	relayInfo := runtime.relayInfo
	if relayInfo == nil {
		relayInfo = &relaycommon.RelayInfo{
			RequestId:       common.GetContextKeyString(runtime.context, common.RequestIdKey),
			UserId:          lifecycle.identity.userId,
			UserGroup:       lifecycle.identity.userGroup,
			UsingGroup:      lifecycle.identity.usingGroup,
			TokenId:         lifecycle.identity.tokenId,
			OriginModelName: record.Model,
			StartTime:       time.Now(),
		}
	}
	return lifecycle.dependencies.recordError(runtime.context, relayInfo, record.ResultCode, map[string]interface{}{
		"request_purpose":            record.RequestPurpose,
		"parent_request_id":          record.ParentRequestID,
		"policy_version":             record.PolicyVersion,
		"source_digest":              record.SourceDigest,
		"prepared_request_digest":    record.PreparedRequestDigest,
		"summary_digest":             record.SummaryDigest,
		"result_code":                record.ResultCode,
		"billable_execution_failure": record.BillableExecutionFailure,
	})
}

func (lifecycle *internalCompactionLifecycle) newChildContext(parentExecution context.Context, descriptor contextconsensus.CompactionChildDescriptor, request *dto.GeneralOpenAIRequest) (*gin.Context, *boundedInternalResponseWriter, context.CancelFunc, error) {
	baseContext, cancel := context.WithTimeout(context.Background(), lifecycle.timeout)
	stopParentCancellation := context.AfterFunc(parentExecution, cancel)
	combinedCancel := func() {
		stopParentCancellation()
		cancel()
	}
	body, err := common.Marshal(request)
	if err != nil {
		combinedCancel()
		return nil, nil, nil, fmt.Errorf("marshal child request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(baseContext, http.MethodPost, internalCompactionRequestPath, bytes.NewReader(body))
	if err != nil {
		combinedCancel()
		return nil, nil, nil, fmt.Errorf("create child request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	responseWriter := newBoundedInternalResponseWriter(lifecycle.maxResponseBytes)
	childContext, _ := gin.CreateTestContext(responseWriter)
	childContext.Request = httpRequest
	copyInternalCompactionIdentity(childContext, lifecycle.identity)
	common.SetContextKey(childContext, common.RequestIdKey, descriptor.ChildRequestID)
	common.SetContextKey(childContext, constant.ContextKeyOriginalModel, descriptor.Model)
	common.SetContextKey(childContext, constant.ContextKeyRequestStartTime, time.Now())
	common.SetContextKey(childContext, constant.ContextKeySuppressDebugLog, true)
	return childContext, responseWriter, combinedCancel, nil
}

func (lifecycle *internalCompactionLifecycle) getRuntime() (*internalCompactionRuntime, error) {
	lifecycle.mutex.Lock()
	defer lifecycle.mutex.Unlock()
	if lifecycle.runtime == nil {
		return nil, fmt.Errorf("internal compaction runtime is unavailable")
	}
	return lifecycle.runtime, nil
}

func captureInternalCompactionIdentity(parent *gin.Context) internalCompactionIdentity {
	identity := internalCompactionIdentity{
		userId:                 common.GetContextKeyInt(parent, constant.ContextKeyUserId),
		userGroup:              common.GetContextKeyString(parent, constant.ContextKeyUserGroup),
		usingGroup:             common.GetContextKeyString(parent, constant.ContextKeyUsingGroup),
		userQuota:              common.GetContextKeyInt(parent, constant.ContextKeyUserQuota),
		userEmail:              common.GetContextKeyString(parent, constant.ContextKeyUserEmail),
		userName:               common.GetContextKeyString(parent, constant.ContextKeyUserName),
		tokenId:                common.GetContextKeyInt(parent, constant.ContextKeyTokenId),
		tokenKey:               common.GetContextKeyString(parent, constant.ContextKeyTokenKey),
		tokenName:              parent.GetString("token_name"),
		tokenQuota:             parent.GetInt("token_quota"),
		tokenUnlimited:         common.GetContextKeyBool(parent, constant.ContextKeyTokenUnlimited),
		tokenGroup:             common.GetContextKeyString(parent, constant.ContextKeyTokenGroup),
		tokenModelLimitEnabled: common.GetContextKeyBool(parent, constant.ContextKeyTokenModelLimitEnabled),
	}
	identity.userSetting, _ = common.GetContextKeyType[dto.UserSetting](parent, constant.ContextKeyUserSetting)
	if rawLimit, ok := common.GetContextKey(parent, constant.ContextKeyTokenModelLimit); ok {
		if tokenModelLimit, ok := rawLimit.(map[string]bool); ok {
			identity.tokenModelLimit = make(map[string]bool, len(tokenModelLimit))
			for modelName, allowed := range tokenModelLimit {
				identity.tokenModelLimit[modelName] = allowed
			}
		}
	}
	if identity.tokenGroup == "" {
		identity.tokenGroup = identity.usingGroup
	}
	if identity.tokenGroup == "" {
		identity.tokenGroup = identity.userGroup
	}
	return identity
}

func copyInternalCompactionIdentity(child *gin.Context, identity internalCompactionIdentity) {
	common.SetContextKey(child, constant.ContextKeyUserId, identity.userId)
	common.SetContextKey(child, constant.ContextKeyUserGroup, identity.userGroup)
	common.SetContextKey(child, constant.ContextKeyUsingGroup, identity.usingGroup)
	common.SetContextKey(child, constant.ContextKeyUserQuota, identity.userQuota)
	common.SetContextKey(child, constant.ContextKeyUserEmail, identity.userEmail)
	common.SetContextKey(child, constant.ContextKeyUserName, identity.userName)
	common.SetContextKey(child, constant.ContextKeyUserSetting, identity.userSetting)
	common.SetContextKey(child, constant.ContextKeyTokenId, identity.tokenId)
	common.SetContextKey(child, constant.ContextKeyTokenKey, identity.tokenKey)
	common.SetContextKey(child, constant.ContextKeyTokenUnlimited, identity.tokenUnlimited)
	common.SetContextKey(child, constant.ContextKeyTokenGroup, identity.tokenGroup)
	common.SetContextKey(child, constant.ContextKeyTokenModelLimitEnabled, identity.tokenModelLimitEnabled)
	if identity.tokenModelLimit != nil {
		common.SetContextKey(child, constant.ContextKeyTokenModelLimit, identity.tokenModelLimit)
	}
	child.Set("token_name", identity.tokenName)
	child.Set("token_quota", identity.tokenQuota)
}

func freezeInternalCompactionModelPool(models []string) (map[string]struct{}, error) {
	if len(models) == 0 {
		return nil, fmt.Errorf("compaction model pool is required")
	}
	pool := make(map[string]struct{}, len(models))
	for _, modelName := range models {
		if err := contextconsensus.ValidateExplicitCompactionModel(modelName); err != nil {
			return nil, err
		}
		pool[ratio_setting.FormatMatchingModelName(strings.TrimSpace(modelName))] = struct{}{}
	}
	return pool, nil
}

func freezeInternalCompactionChannelIDs(channelIDs []int) (map[int]struct{}, error) {
	if len(channelIDs) == 0 {
		return nil, fmt.Errorf("allowed compaction channel IDs are required")
	}
	allowed := make(map[int]struct{}, len(channelIDs))
	for _, channelID := range channelIDs {
		if channelID <= 0 {
			return nil, fmt.Errorf("allowed compaction channel ID must be positive")
		}
		allowed[channelID] = struct{}{}
	}
	return allowed, nil
}

func normalizeInternalCompactionRequest(source *dto.GeneralOpenAIRequest, modelName string, maxOutputTokens int) (*dto.GeneralOpenAIRequest, error) {
	if err := contextconsensus.ValidateExplicitCompactionModel(modelName); err != nil {
		return nil, err
	}
	if maxOutputTokens <= 0 {
		return nil, fmt.Errorf("compaction max output tokens must be positive")
	}
	if len(source.Messages) == 0 {
		return nil, fmt.Errorf("compaction prompt messages are required")
	}
	messages := make([]dto.Message, 0, len(source.Messages))
	for index := range source.Messages {
		message := source.Messages[index]
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "system" && role != "user" {
			return nil, fmt.Errorf("compaction prompt message %d has unsupported role", index)
		}
		if !message.IsStringContent() || strings.TrimSpace(message.StringContent()) == "" {
			return nil, fmt.Errorf("compaction prompt message %d must contain text only", index)
		}
		messages = append(messages, dto.Message{Role: role, Content: message.StringContent()})
	}
	stream := false
	maxTokens := uint(maxOutputTokens)
	return &dto.GeneralOpenAIRequest{
		Model:               strings.TrimSpace(modelName),
		Messages:            messages,
		Stream:              &stream,
		MaxCompletionTokens: &maxTokens,
	}, nil
}

func recordInternalCompactionError(ctx *gin.Context, info *relaycommon.RelayInfo, resultCode string, other map[string]interface{}) error {
	if !constant.ErrorLogEnabled {
		return fmt.Errorf("internal compaction error logging is disabled")
	}
	return model.RecordErrorLogResult(
		ctx,
		info.UserId,
		info.ChannelId,
		info.OriginModelName,
		ctx.GetString("token_name"),
		"internal context compaction failed: "+resultCode,
		info.TokenId,
		int(time.Since(info.StartTime).Seconds()),
		false,
		info.UsingGroup,
		other,
	)
}
