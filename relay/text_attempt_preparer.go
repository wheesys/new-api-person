package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
)

type preparedTextResponseMode int

const (
	preparedTextResponseModeAdaptor preparedTextResponseMode = iota
	preparedTextResponseModeChatViaResponses
	preparedTextResponseModeResponsesViaChat
)

// PreparedTextRelayAttempt owns the exact request snapshot and adaptor used by
// one text relay attempt. Close releases the snapshot and restores temporary
// protocol state after a Chat Completions-to-Responses attempt.
type PreparedTextRelayAttempt struct {
	info                *relaycommon.RelayInfo
	adaptor             channel.Adaptor
	preparedRequest     *relaycommon.PreparedRelayRequest
	responseMode        preparedTextResponseMode
	detectEventStream   bool
	requestErrorPrefix  string
	authoritativeTarget *channel.TextRelayTarget
	passThrough         bool
	restoreProtocolMode bool
	savedRelayMode      int
	savedRequestURLPath string
	lifecycleMutex      sync.Mutex
	executed            bool
	closed              bool
	closeOnce           sync.Once
	closeErr            error
}

type textAttemptPreparationPolicy struct {
	authoritative bool
}

var (
	compatibleTextAttemptPolicy    = textAttemptPreparationPolicy{}
	authoritativeTextAttemptPolicy = textAttemptPreparationPolicy{authoritative: true}
)

func textRelayTargetInput(info *relaycommon.RelayInfo) (channel.TextRelayTargetInput, error) {
	requestPath, err := relaycommon.AuthoritativeTextRequestPath(info.RequestURLPath)
	if err != nil {
		return channel.TextRelayTargetInput{}, err
	}
	return channel.TextRelayTargetInput{
		ChannelType:                  info.ChannelType,
		OriginModel:                  info.OriginModelName,
		UpstreamModel:                info.UpstreamModelName,
		SourceProtocol:               info.RelayFormat,
		FinalProtocol:                info.GetFinalRequestRelayFormat(),
		RelayMode:                    info.RelayMode,
		RequestURLPath:               requestPath,
		GeminiThinkingAdapterEnabled: model_setting.GetGeminiSettings().ThinkingAdapterEnabled,
		PreserveThinkingSuffix:       model_setting.ShouldPreserveThinkingSuffix(info.OriginModelName),
	}, nil
}

func validateAuthoritativeTextPreparation(info *relaycommon.RelayInfo, adaptor channel.Adaptor, policy textAttemptPreparationPolicy) *types.NewAPIError {
	if !policy.authoritative {
		return nil
	}
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		return types.NewErrorWithStatusCode(fmt.Errorf("pass-through requests cannot produce authoritative text evidence"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	authoritativeAdaptor, ok := adaptor.(channel.AuthoritativeTextRelayAdaptor)
	if !ok {
		return types.NewErrorWithStatusCode(fmt.Errorf("adaptor does not declare authoritative text preparation capabilities"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	input, err := textRelayTargetInput(info)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	capabilities := authoritativeAdaptor.TextRelayPreparationCapabilities(input)
	if !capabilities.OfflineConversion || !capabilities.PureTargetResolution {
		return types.NewErrorWithStatusCode(fmt.Errorf("adaptor does not support authoritative offline conversion and pure target resolution"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	return nil
}

func resolveAuthoritativeTextTarget(info *relaycommon.RelayInfo, adaptor channel.Adaptor, policy textAttemptPreparationPolicy) (*channel.TextRelayTarget, *types.NewAPIError) {
	if !policy.authoritative {
		return nil, nil
	}
	authoritativeAdaptor, ok := adaptor.(channel.AuthoritativeTextRelayAdaptor)
	if !ok {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("adaptor does not declare authoritative text preparation capabilities"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	input, inputErr := textRelayTargetInput(info)
	if inputErr != nil {
		return nil, types.NewError(inputErr, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	target, err := authoritativeAdaptor.ResolveTextRelayTarget(input)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if target.Model == "" || target.Protocol == "" {
		return nil, types.NewError(fmt.Errorf("resolved authoritative text target is incomplete"), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if target.Protocol != input.FinalProtocol || target.RelayMode != input.RelayMode || target.RequestURLPath != input.RequestURLPath {
		return nil, types.NewError(fmt.Errorf("resolved authoritative text target changed protocol, mode, or path"), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	info.UpstreamModelName = target.Model
	return &target, nil
}

func sealAuthoritativeTextTarget(info *relaycommon.RelayInfo, request *relaycommon.PreparedRelayRequest, target *channel.TextRelayTarget) *types.NewAPIError {
	if target == nil {
		return nil
	}
	err := info.SealAuthoritativeTextTarget(relaycommon.AuthoritativeTextTarget{
		Model:          target.Model,
		Protocol:       target.Protocol,
		RelayMode:      target.RelayMode,
		RequestURLPath: target.RequestURLPath,
	}, request)
	if err == nil {
		return nil
	}
	_ = request.Close()
	info.ResetPreparedRelayRequest()
	return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
}

// PreparedRequest returns the immutable upstream request snapshot that will be
// consumed by ExecutePreparedTextRelayAttempt.
func (attempt *PreparedTextRelayAttempt) PreparedRequest() *relaycommon.PreparedRelayRequest {
	if attempt == nil {
		return nil
	}
	return attempt.preparedRequest
}

// AuthoritativeTarget returns a copy so callers cannot mutate the target seal.
func (attempt *PreparedTextRelayAttempt) AuthoritativeTarget() (channel.TextRelayTarget, bool) {
	if attempt == nil || attempt.authoritativeTarget == nil {
		return channel.TextRelayTarget{}, false
	}
	return *attempt.authoritativeTarget, true
}

// ValidateManagedProviderStateRequest verifies the exact frozen request that
// will be sent upstream before a response ID can become managed provider state.
func (attempt *PreparedTextRelayAttempt) ValidateManagedProviderStateRequest(resolution *contextconsensus.ManagedProviderStateResolution) error {
	if attempt == nil || attempt.adaptor == nil || attempt.info == nil || attempt.preparedRequest == nil {
		return fmt.Errorf("managed provider state request is unavailable")
	}
	if _, ok := attempt.adaptor.(channel.ManagedProviderStateReportingAdaptor); !ok {
		return fmt.Errorf("text relay adaptor does not report managed provider state")
	}
	if attempt.info.IsStream || attempt.info.RelayFormat != types.RelayFormatOpenAIResponses ||
		attempt.info.GetFinalRequestRelayFormat() != types.RelayFormatOpenAIResponses || attempt.info.ChannelType != constant.ChannelTypeOpenAI {
		return fmt.Errorf("managed provider state requires native non-streaming OpenAI Responses")
	}
	for headerName := range relaycommon.GetEffectiveHeaderOverride(attempt.info) {
		normalizedHeader := strings.ToLower(strings.TrimSpace(headerName))
		if normalizedHeader == "authorization" || normalizedHeader == "api-key" || normalizedHeader == "x-api-key" {
			return fmt.Errorf("managed provider state does not allow credential header overrides")
		}
	}
	body, err := attempt.preparedRequest.Body()
	if err != nil {
		return err
	}
	var request dto.OpenAIResponsesRequest
	if err := common.Unmarshal(body, &request); err != nil {
		return fmt.Errorf("decode managed provider state request: %w", err)
	}
	if err := contextconsensus.ValidateManagedResponsesProviderStateFields(body); err != nil {
		return err
	}
	if len(request.Store) > 0 {
		var store bool
		if err := common.Unmarshal(request.Store, &store); err != nil || !store {
			return fmt.Errorf("managed provider state requires a storable native Responses request")
		}
	}
	if resolution == nil {
		if request.PreviousResponseID != "" {
			return fmt.Errorf("managed provider state request has no authenticated binding")
		}
		return nil
	}
	return resolution.ValidateStateReference(request.PreviousResponseID)
}

type providerFileLifecycleResolution interface {
	ValidateFinalBody(body []byte) error
	ValidateFinalTarget(channelID, channelType, multiKeyIndex int, channelIsMultiKey bool, endpoint, organization, project, credential string) error
}

// ValidateProviderFileLifecycleRequest verifies provider-owned file references
// from the immutable body that will be sent upstream. It performs no network IO.
func (attempt *PreparedTextRelayAttempt) ValidateProviderFileLifecycleRequest(resolutions ...providerFileLifecycleResolution) (contextconsensus.ProviderFileState, error) {
	emptyState := contextconsensus.ProviderFileState{}
	if attempt == nil || attempt.adaptor == nil || attempt.info == nil || attempt.preparedRequest == nil {
		return emptyState, fmt.Errorf("provider file lifecycle request is unavailable")
	}
	body, err := attempt.preparedRequest.Body()
	if err != nil {
		return emptyState, err
	}
	protocol := attempt.preparedRequest.Protocol()
	state, err := contextconsensus.ExtractProviderFileState(contextconsensus.ExtractionRequest{
		Protocol: protocol,
		Body:     body,
	})
	if err != nil {
		return emptyState, err
	}
	if !state.RequiresLifecycleValidation() {
		return state, nil
	}
	if state.Count(contextconsensus.ProviderFileReferenceSignedURL) > 0 {
		return emptyState, fmt.Errorf("signed provider file URLs do not have authoritative lifecycle evidence")
	}
	if attempt.passThrough || attempt.info.RelayFormat != protocol || attempt.info.GetFinalRequestRelayFormat() != protocol ||
		len(attempt.info.RequestConversionChain) != 1 || attempt.info.RequestConversionChain[0] != protocol {
		return emptyState, fmt.Errorf("provider file lifecycle requires a native request without pass-through or protocol conversion")
	}
	for headerName := range relaycommon.GetEffectiveHeaderOverride(attempt.info) {
		switch strings.ToLower(strings.TrimSpace(headerName)) {
		case "authorization", "proxy-authorization", "api-key", "x-api-key", "x-goog-api-key", "anthropic-api-key":
			return emptyState, fmt.Errorf("provider file lifecycle does not allow credential header overrides")
		}
	}
	if len(resolutions) > 1 {
		return emptyState, fmt.Errorf("provider file lifecycle resolution is ambiguous")
	}
	if len(resolutions) == 1 {
		resolution := resolutions[0]
		if resolution == nil || resolution.ValidateFinalBody(body) != nil || resolution.ValidateFinalTarget(
			attempt.info.ChannelId, attempt.info.ChannelType, attempt.info.ChannelMultiKeyIndex, attempt.info.ChannelIsMultiKey,
			attempt.info.ChannelBaseUrl, attempt.info.Organization, attempt.info.Project, attempt.info.ApiKey,
		) != nil {
			return emptyState, fmt.Errorf("provider file lifecycle binding does not match the final request")
		}
		return state, nil
	}
	lifecycleAdaptor, ok := attempt.adaptor.(channel.ProviderFileLifecycleAdaptor)
	if !ok {
		return emptyState, fmt.Errorf("text relay adaptor does not declare provider file lifecycle capabilities")
	}
	capabilityState := state
	capabilityState.References = append([]contextconsensus.ProviderFileReferenceEvidence(nil), state.References...)
	capabilityState.ReasonCodes = append([]string(nil), state.ReasonCodes...)
	capabilities := lifecycleAdaptor.ProviderFileLifecycleCapabilities(attempt.info, capabilityState)
	if !capabilities.Complete() {
		return emptyState, fmt.Errorf("text relay adaptor does not provide complete authoritative provider file lifecycle capabilities")
	}
	return state, nil
}

func (attempt *PreparedTextRelayAttempt) ExtractManagedProviderStateReport(httpStatus int, responseBody []byte) (contextconsensus.ManagedProviderStateReport, error) {
	if attempt == nil || attempt.adaptor == nil || attempt.info == nil {
		return contextconsensus.ManagedProviderStateReport{}, fmt.Errorf("managed provider state reporting adaptor is unavailable")
	}
	reporter, ok := attempt.adaptor.(channel.ManagedProviderStateReportingAdaptor)
	if !ok {
		return contextconsensus.ManagedProviderStateReport{}, fmt.Errorf("text relay adaptor does not report managed provider state")
	}
	return reporter.ExtractManagedProviderStateReport(attempt.info, httpStatus, responseBody)
}

// Close is idempotent. For Chat-to-Responses it also restores the caller's
// original relay mode and request path after response handling has completed.
func (attempt *PreparedTextRelayAttempt) Close() error {
	if attempt == nil {
		return nil
	}
	attempt.closeOnce.Do(func() {
		attempt.lifecycleMutex.Lock()
		defer attempt.lifecycleMutex.Unlock()
		attempt.closed = true
		if attempt.authoritativeTarget != nil && attempt.info != nil {
			attempt.info.ClearAuthoritativeTextTarget()
		}
		if attempt.preparedRequest != nil {
			attempt.closeErr = attempt.preparedRequest.Close()
		}
		if attempt.restoreProtocolMode && attempt.info != nil {
			attempt.info.RelayMode = attempt.savedRelayMode
			attempt.info.RequestURLPath = attempt.savedRequestURLPath
		}
	})
	return attempt.closeErr
}

// PrepareTextRelayAttempt prepares one supported text attempt without
// performing network I/O or response handling.
func PrepareTextRelayAttempt(c *gin.Context, info *relaycommon.RelayInfo) (*PreparedTextRelayAttempt, *types.NewAPIError) {
	return prepareTextRelayAttempt(c, info, compatibleTextAttemptPolicy)
}

// PrepareAuthoritativeTextRelayAttempt requires explicit adaptor opt-in and
// seals the final model, protocol, mode, path, and request snapshot.
func PrepareAuthoritativeTextRelayAttempt(c *gin.Context, info *relaycommon.RelayInfo) (*PreparedTextRelayAttempt, *types.NewAPIError) {
	return prepareTextRelayAttempt(c, info, authoritativeTextAttemptPolicy)
}

func prepareTextRelayAttempt(c *gin.Context, info *relaycommon.RelayInfo, policy textAttemptPreparationPolicy) (*PreparedTextRelayAttempt, *types.NewAPIError) {
	if info == nil {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("relay info is required"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	switch info.RelayFormat {
	case types.RelayFormatOpenAI,
		types.RelayFormatOpenAIResponses,
		types.RelayFormatOpenAIResponsesCompaction,
		types.RelayFormatClaude,
		types.RelayFormatGemini:
	default:
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("unsupported text relay format: %s", info.RelayFormat), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	info.InitChannelMeta(c)
	info.ResetPreparedRelayRequest()

	switch info.RelayFormat {
	case types.RelayFormatOpenAI:
		return prepareOpenAITextAttemptWithAdaptorPolicy(c, info, nil, policy)
	case types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIResponsesCompaction:
		return prepareResponsesTextAttemptWithAdaptorPolicy(c, info, nil, policy)
	case types.RelayFormatClaude:
		return prepareClaudeTextAttemptWithAdaptorPolicy(c, info, nil, policy)
	case types.RelayFormatGemini:
		return prepareGeminiTextAttemptWithAdaptorPolicy(c, info, nil, policy)
	default:
		panic("validated text relay format was not dispatched")
	}
}

func prepareOpenAITextAttemptWithAdaptor(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor) (*PreparedTextRelayAttempt, *types.NewAPIError) {
	return prepareOpenAITextAttemptWithAdaptorPolicy(c, info, adaptor, compatibleTextAttemptPolicy)
}

func prepareOpenAITextAttemptWithAdaptorPolicy(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, policy textAttemptPreparationPolicy) (*PreparedTextRelayAttempt, *types.NewAPIError) {
	textRequest, ok := info.Request.(*dto.GeneralOpenAIRequest)
	if !ok {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("invalid request type, expected dto.GeneralOpenAIRequest, got %T", info.Request), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	request, err := common.DeepCopy(textRequest)
	if err != nil {
		return nil, types.NewError(fmt.Errorf("failed to copy request to GeneralOpenAIRequest: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	if request.WebSearchOptions != nil {
		c.Set("chat_completion_web_search_context_size", request.WebSearchOptions.SearchContextSize)
	}

	if err = helper.ModelMappedHelper(c, info, request); err != nil {
		return nil, types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	includeUsage := true
	if request.StreamOptions != nil {
		includeUsage = request.StreamOptions.IncludeUsage
	}
	if !info.SupportStreamOptions || !lo.FromPtrOr(request.Stream, false) {
		request.StreamOptions = nil
	} else if constant.ForceStreamOption {
		request.StreamOptions = &dto.StreamOptions{IncludeUsage: true}
	}
	info.ShouldIncludeUsage = includeUsage

	if adaptor == nil {
		adaptor = GetAdaptor(info.ApiType)
		if adaptor == nil {
			return nil, types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
		}
	}
	adaptor.Init(info)
	if newAPIError := validateAuthoritativeTextPreparation(info, adaptor, policy); newAPIError != nil {
		return nil, newAPIError
	}

	passThroughGlobal := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	if info.RelayMode == relayconstant.RelayModeChatCompletions &&
		!passThroughGlobal &&
		!info.ChannelSetting.PassThroughBodyEnabled &&
		service.ShouldChatCompletionsUseResponsesGlobal(info.ChannelId, info.ChannelType, info.OriginModelName) {
		applySystemPromptIfNeeded(c, info, request)
		return prepareChatCompletionsViaResponsesAttemptPolicy(c, info, adaptor, request, policy)
	}

	var preparedRequest *relaycommon.PreparedRelayRequest
	if passThroughGlobal || info.ChannelSetting.PassThroughBodyEnabled {
		storage, storageErr := common.GetBodyStorage(c)
		if storageErr != nil {
			return nil, types.NewErrorWithStatusCode(storageErr, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		if common.DebugEnabled {
			if debugBytes, debugErr := storage.Bytes(); debugErr == nil {
				logger.LogDebug(c, "requestBody: %s", debugBytes)
			}
		}
		preparedRequest, err = relaycommon.PrepareFinalPassThroughRelayRequest(info, storage)
		if err != nil {
			return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
	} else {
		convertedRequest, convertErr := adaptor.ConvertOpenAIRequest(c, info, request)
		if convertErr != nil {
			return nil, types.NewError(convertErr, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		relaycommon.AppendRequestConversionFromRequest(info, convertedRequest)
		authoritativeTarget, targetErr := resolveAuthoritativeTextTarget(info, adaptor, policy)
		if targetErr != nil {
			return nil, targetErr
		}

		if convertedOpenAIRequest, isOpenAIRequest := convertedRequest.(*dto.GeneralOpenAIRequest); isOpenAIRequest {
			applySystemPromptIfNeeded(c, info, convertedOpenAIRequest)
		}

		jsonData, marshalErr := common.Marshal(convertedRequest)
		if marshalErr != nil {
			return nil, types.NewError(marshalErr, types.ErrorCodeJsonMarshalFailed, types.ErrOptionWithSkipRetry())
		}

		jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		if len(info.ParamOverride) > 0 {
			jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
			if err != nil {
				return nil, newAPIErrorFromParamOverride(err)
			}
		}

		logger.LogDebug(c, "text request body: %s", jsonData)
		preparedRequest, err = relaycommon.PrepareFinalJSONRelayRequest(info, jsonData)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		if targetErr = sealAuthoritativeTextTarget(info, preparedRequest, authoritativeTarget); targetErr != nil {
			return nil, targetErr
		}
		return &PreparedTextRelayAttempt{
			info:                info,
			adaptor:             adaptor,
			preparedRequest:     preparedRequest,
			responseMode:        preparedTextResponseModeAdaptor,
			detectEventStream:   true,
			authoritativeTarget: authoritativeTarget,
		}, nil
	}

	return &PreparedTextRelayAttempt{
		info:              info,
		adaptor:           adaptor,
		preparedRequest:   preparedRequest,
		responseMode:      preparedTextResponseModeAdaptor,
		detectEventStream: true,
		passThrough:       true,
	}, nil
}

type convertedTextAttemptOptions struct {
	removeDisabledFields bool
	marshalErrorCode     types.ErrorCode
	logMessage           string
	detectEventStream    bool
	requestErrorPrefix   string
	// restoreProtocolMode restores the caller's original relay mode and request
	// path after the attempt closes. Used when a converted attempt temporarily
	// switches protocol (e.g. Responses downgraded to Chat Completions).
	restoreProtocolMode bool
	savedRelayMode      int
	savedRequestURLPath string
}

func prepareConvertedTextAttempt(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, convertedRequest any, options convertedTextAttemptOptions, policy textAttemptPreparationPolicy) (*PreparedTextRelayAttempt, *types.NewAPIError) {
	relaycommon.AppendRequestConversionFromRequest(info, convertedRequest)
	authoritativeTarget, newAPIError := resolveAuthoritativeTextTarget(info, adaptor, policy)
	if newAPIError != nil {
		return nil, newAPIError
	}
	jsonData, err := common.Marshal(convertedRequest)
	if err != nil {
		return nil, types.NewError(err, options.marshalErrorCode, types.ErrOptionWithSkipRetry())
	}
	if options.removeDisabledFields {
		jsonData, err = relaycommon.RemoveDisabledFields(jsonData, info.ChannelOtherSettings, info.ChannelSetting.PassThroughBodyEnabled)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
	}
	if len(info.ParamOverride) > 0 {
		jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
		if err != nil {
			return nil, newAPIErrorFromParamOverride(err)
		}
	}
	logger.LogDebug(c, options.logMessage, jsonData)

	preparedRequest, err := relaycommon.PrepareFinalJSONRelayRequest(info, jsonData)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	if newAPIError = sealAuthoritativeTextTarget(info, preparedRequest, authoritativeTarget); newAPIError != nil {
		return nil, newAPIError
	}
	return &PreparedTextRelayAttempt{
		info:                info,
		adaptor:             adaptor,
		preparedRequest:     preparedRequest,
		responseMode:        preparedTextResponseModeAdaptor,
		detectEventStream:   options.detectEventStream,
		requestErrorPrefix:  options.requestErrorPrefix,
		authoritativeTarget: authoritativeTarget,
		restoreProtocolMode: options.restoreProtocolMode,
		savedRelayMode:      options.savedRelayMode,
		savedRequestURLPath: options.savedRequestURLPath,
	}, nil
}

func resolveTextAttemptAdaptor(info *relaycommon.RelayInfo, adaptor channel.Adaptor) (channel.Adaptor, *types.NewAPIError) {
	if adaptor == nil {
		adaptor = GetAdaptor(info.ApiType)
		if adaptor == nil {
			return nil, types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
		}
	}
	adaptor.Init(info)
	return adaptor, nil
}

func prepareResponsesTextAttemptWithAdaptor(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor) (*PreparedTextRelayAttempt, *types.NewAPIError) {
	return prepareResponsesTextAttemptWithAdaptorPolicy(c, info, adaptor, compatibleTextAttemptPolicy)
}

func prepareResponsesTextAttemptWithAdaptorPolicy(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, policy textAttemptPreparationPolicy) (*PreparedTextRelayAttempt, *types.NewAPIError) {
	if info.RelayMode == relayconstant.RelayModeResponsesCompact &&
		!common.SupportsResponsesCompact(info.ChannelType, info.ApiType) {
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("unsupported endpoint %q for api type %d", "/v1/responses/compact", info.ApiType),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	var responsesRequest *dto.OpenAIResponsesRequest
	switch request := info.Request.(type) {
	case *dto.OpenAIResponsesRequest:
		responsesRequest = request
	case *dto.OpenAIResponsesCompactionRequest:
		responsesRequest = &dto.OpenAIResponsesRequest{
			Model:                request.Model,
			Input:                request.Input,
			Instructions:         request.Instructions,
			PreviousResponseID:   request.PreviousResponseID,
			ParallelToolCalls:    request.ParallelToolCalls,
			ServiceTier:          request.ServiceTier,
			PromptCacheKey:       request.PromptCacheKey,
			PromptCacheOptions:   request.PromptCacheOptions,
			PromptCacheRetention: request.PromptCacheRetention,
		}
	default:
		return nil, types.NewErrorWithStatusCode(
			fmt.Errorf("invalid request type, expected dto.OpenAIResponsesRequest or dto.OpenAIResponsesCompactionRequest, got %T", info.Request),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	request, err := common.DeepCopy(responsesRequest)
	if err != nil {
		return nil, types.NewError(fmt.Errorf("failed to copy request to GeneralOpenAIRequest: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	if err = helper.ModelMappedHelper(c, info, request); err != nil {
		return nil, types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	adaptor, newAPIError := resolveTextAttemptAdaptor(info, adaptor)
	if newAPIError != nil {
		return nil, newAPIError
	}
	if newAPIError = validateAuthoritativeTextPreparation(info, adaptor, policy); newAPIError != nil {
		return nil, newAPIError
	}
	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		storage, storageErr := common.GetBodyStorage(c)
		if storageErr != nil {
			return nil, types.NewError(storageErr, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
		}
		preparedRequest, prepareErr := relaycommon.PrepareFinalPassThroughRelayRequest(info, storage)
		if prepareErr != nil {
			return nil, types.NewError(prepareErr, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
		}
		return &PreparedTextRelayAttempt{
			info:            info,
			adaptor:         adaptor,
			preparedRequest: preparedRequest,
			responseMode:    preparedTextResponseModeAdaptor,
			passThrough:     true,
		}, nil
	}

	convertedRequest, err := adaptor.ConvertOpenAIResponsesRequest(c, info, *request)
	if err != nil {
		if errors.Is(err, relaycommon.ErrResponsesNotSupported) {
			return prepareResponsesAsChatFallback(c, info, adaptor, request, policy)
		}
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	return prepareConvertedTextAttempt(c, info, adaptor, convertedRequest, convertedTextAttemptOptions{
		removeDisabledFields: true,
		marshalErrorCode:     types.ErrorCodeConvertRequestFailed,
		logMessage:           "requestBody: %s",
		detectEventStream:    false,
	}, policy)
}

// prepareResponsesAsChatFallback downgrades a Responses request to Chat
// Completions when the selected channel does not support the Responses API.
// It reuses the built-in Responses→Chat converter, temporarily switches the
// relay mode so the upstream URL and response handling follow the Chat path,
// and restores the original protocol state on attempt close.
func prepareResponsesAsChatFallback(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, request *dto.OpenAIResponsesRequest, policy textAttemptPreparationPolicy) (*PreparedTextRelayAttempt, *types.NewAPIError) {
	savedRelayMode := info.RelayMode
	savedRequestURLPath := info.RequestURLPath

	if request != nil && len(request.Tools) > 0 {
		logger.LogInfo(c, fmt.Sprintf("responses-as-chat fallback: raw client tools JSON (channel %d): %s", info.ChannelId, string(request.Tools)))
	} else {
		logger.LogInfo(c, fmt.Sprintf("responses-as-chat fallback: raw client tools EMPTY (channel %d)", info.ChannelId))
	}

	result, err := service.ConvertRequestByID(c, info, relayconvert.ConverterOpenAIResponsesToOpenAIChat, request)
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	chatRequest, ok := result.Value.(*dto.GeneralOpenAIRequest)
	if !ok {
		return nil, types.NewError(fmt.Errorf("expected OpenAI chat completions request, got %T", result.Value), types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}

	markResponsesChatFallback(c, info)

	info.RelayMode = relayconstant.RelayModeChatCompletions
	info.RequestURLPath = "/v1/chat/completions"

	convertedRequest, err := adaptor.ConvertOpenAIRequest(c, info, chatRequest)
	if err != nil {
		info.RelayMode = savedRelayMode
		info.RequestURLPath = savedRequestURLPath
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	attempt, newAPIError := prepareConvertedTextAttempt(c, info, adaptor, convertedRequest, convertedTextAttemptOptions{
		removeDisabledFields: true,
		marshalErrorCode:     types.ErrorCodeConvertRequestFailed,
		logMessage:           "requestBody: %s",
		detectEventStream:    false,
		restoreProtocolMode:  true,
		savedRelayMode:       savedRelayMode,
		savedRequestURLPath:  savedRequestURLPath,
	}, policy)
	if newAPIError != nil {
		return nil, newAPIError
	}
	if chatReq, ok := convertedRequest.(*dto.GeneralOpenAIRequest); ok && chatReq != nil {
		names := make([]string, 0, len(chatReq.Tools))
		for _, tool := range chatReq.Tools {
			if tool.Function.Name != "" {
				names = append(names, tool.Type+":"+tool.Function.Name)
			} else {
				names = append(names, tool.Type)
			}
		}
		logger.LogInfo(c, fmt.Sprintf("responses-as-chat fallback: upstream tools (channel %d): [%s]", info.ChannelId, strings.Join(names, ", ")))
	}
	// The upstream answers in Chat format; forward it back to the Codex client
	// as a Responses response.
	attempt.responseMode = preparedTextResponseModeResponsesViaChat
	return attempt, nil
}

// markResponsesChatFallback records that this attempt downgraded from the
// Responses API to Chat Completions so the tool-conversion layer can enable
// codex-style tool mapping.
func markResponsesChatFallback(c *gin.Context, info *relaycommon.RelayInfo) {
	if info == nil {
		return
	}
	info.ResponsesChatFallback = true
	if opts := info.ConvOptions(); opts != nil {
		opts.Codex.ResponsesChatFallback = true
	}
	logger.LogWarn(c, fmt.Sprintf("responses request downgraded to chat completions for channel %d (api type %d)", info.ChannelId, info.ApiType))
}

func prepareClaudeTextAttemptWithAdaptor(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor) (*PreparedTextRelayAttempt, *types.NewAPIError) {
	return prepareClaudeTextAttemptWithAdaptorPolicy(c, info, adaptor, compatibleTextAttemptPolicy)
}

func prepareClaudeTextAttemptWithAdaptorPolicy(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, policy textAttemptPreparationPolicy) (*PreparedTextRelayAttempt, *types.NewAPIError) {
	claudeRequest, ok := info.Request.(*dto.ClaudeRequest)
	if !ok {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("invalid request type, expected *dto.ClaudeRequest, got %T", info.Request), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	request, err := common.DeepCopy(claudeRequest)
	if err != nil {
		return nil, types.NewError(fmt.Errorf("failed to copy request to ClaudeRequest: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	if err = helper.ModelMappedHelper(c, info, request); err != nil {
		return nil, types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	adaptor, newAPIError := resolveTextAttemptAdaptor(info, adaptor)
	if newAPIError != nil {
		return nil, newAPIError
	}
	if newAPIError = validateAuthoritativeTextPreparation(info, adaptor, policy); newAPIError != nil {
		return nil, newAPIError
	}
	if request.MaxTokens == nil {
		defaultMaxTokens := uint(model_setting.GetClaudeSettings().GetDefaultMaxTokens(request.Model))
		request.MaxTokens = &defaultMaxTokens
	}

	if baseModel, effortLevel, hasEffortSuffix := reasoning.TrimEffortSuffix(request.Model); hasEffortSuffix && effortLevel != "" &&
		(strings.HasPrefix(request.Model, "claude-opus-4-6") ||
			strings.HasPrefix(request.Model, "claude-opus-4-7") ||
			strings.HasPrefix(request.Model, "claude-opus-4-8")) {
		request.Model = baseModel
		request.Thinking = &dto.Thinking{Type: "adaptive"}
		request.OutputConfig = json.RawMessage(fmt.Sprintf(`{"effort":"%s"}`, effortLevel))
		if strings.HasPrefix(request.Model, "claude-opus-4-7") || strings.HasPrefix(request.Model, "claude-opus-4-8") {
			request.Thinking.Display = "summarized"
			request.Temperature = nil
			request.TopP = nil
			request.TopK = nil
		} else {
			request.Temperature = common.GetPointer[float64](1.0)
		}
		info.UpstreamModelName = request.Model
	} else if model_setting.GetClaudeSettings().ThinkingAdapterEnabled && strings.HasSuffix(request.Model, "-thinking") {
		if request.Thinking == nil {
			baseModel := strings.TrimSuffix(request.Model, "-thinking")
			if strings.HasPrefix(baseModel, "claude-opus-4-7") || strings.HasPrefix(baseModel, "claude-opus-4-8") {
				request.Thinking = &dto.Thinking{Type: "adaptive", Display: "summarized"}
				request.OutputConfig = json.RawMessage(`{"effort":"high"}`)
				request.Temperature = nil
				request.TopP = nil
				request.TopK = nil
			} else {
				if request.MaxTokens == nil || *request.MaxTokens < 1280 {
					request.MaxTokens = common.GetPointer[uint](1280)
				}
				request.Thinking = &dto.Thinking{
					Type:         "enabled",
					BudgetTokens: common.GetPointer[int](int(float64(*request.MaxTokens) * model_setting.GetClaudeSettings().ThinkingAdapterBudgetTokensPercentage)),
				}
				request.Temperature = common.GetPointer[float64](1.0)
			}
		}
		if !model_setting.ShouldPreserveThinkingSuffix(info.OriginModelName) {
			request.Model = strings.TrimSuffix(request.Model, "-thinking")
		}
		info.UpstreamModelName = request.Model
	}

	if info.ChannelSetting.SystemPrompt != "" {
		if request.System == nil {
			request.SetStringSystem(info.ChannelSetting.SystemPrompt)
		} else if info.ChannelSetting.SystemPromptOverride {
			common.SetContextKey(c, constant.ContextKeySystemPromptOverride, true)
			if request.IsStringSystem() {
				existingSystem := strings.TrimSpace(request.GetStringSystem())
				if existingSystem == "" {
					request.SetStringSystem(info.ChannelSetting.SystemPrompt)
				} else {
					request.SetStringSystem(info.ChannelSetting.SystemPrompt + "\n" + existingSystem)
				}
			} else {
				systemContents := request.ParseSystem()
				newSystem := dto.ClaudeMediaMessage{Type: dto.ContentTypeText}
				newSystem.SetText(info.ChannelSetting.SystemPrompt)
				if len(systemContents) == 0 {
					request.System = []dto.ClaudeMediaMessage{newSystem}
				} else {
					request.System = append([]dto.ClaudeMediaMessage{newSystem}, systemContents...)
				}
			}
		}
	}

	if !model_setting.GetGlobalSettings().PassThroughRequestEnabled &&
		!info.ChannelSetting.PassThroughBodyEnabled &&
		service.ShouldChatCompletionsUseResponsesGlobal(info.ChannelId, info.ChannelType, info.OriginModelName) {
		openAIRequest, convertErr := service.ClaudeToOpenAIRequest(*request, info)
		if convertErr != nil {
			return nil, types.NewError(convertErr, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		return prepareChatCompletionsViaResponsesAttemptPolicy(c, info, adaptor, openAIRequest, policy)
	}

	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		storage, storageErr := common.GetBodyStorage(c)
		if storageErr != nil {
			return nil, types.NewErrorWithStatusCode(storageErr, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		preparedRequest, prepareErr := relaycommon.PrepareFinalPassThroughRelayRequest(info, storage)
		if prepareErr != nil {
			return nil, types.NewErrorWithStatusCode(prepareErr, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		return &PreparedTextRelayAttempt{
			info:              info,
			adaptor:           adaptor,
			preparedRequest:   preparedRequest,
			responseMode:      preparedTextResponseModeAdaptor,
			detectEventStream: true,
			passThrough:       true,
		}, nil
	}

	convertedRequest, err := adaptor.ConvertClaudeRequest(c, info, request)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	return prepareConvertedTextAttempt(c, info, adaptor, convertedRequest, convertedTextAttemptOptions{
		removeDisabledFields: true,
		marshalErrorCode:     types.ErrorCodeConvertRequestFailed,
		logMessage:           "requestBody: %s",
		detectEventStream:    true,
	}, policy)
}

func isNoThinkingGeminiRequest(request *dto.GeminiChatRequest) bool {
	if request.GenerationConfig.ThinkingConfig == nil || request.GenerationConfig.ThinkingConfig.ThinkingBudget == nil {
		return false
	}
	return *request.GenerationConfig.ThinkingConfig.ThinkingBudget == 0
}

func prepareGeminiTextAttemptWithAdaptor(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor) (*PreparedTextRelayAttempt, *types.NewAPIError) {
	return prepareGeminiTextAttemptWithAdaptorPolicy(c, info, adaptor, compatibleTextAttemptPolicy)
}

func prepareGeminiTextAttemptWithAdaptorPolicy(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, policy textAttemptPreparationPolicy) (*PreparedTextRelayAttempt, *types.NewAPIError) {
	geminiRequest, ok := info.Request.(*dto.GeminiChatRequest)
	if !ok {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("invalid request type, expected *dto.GeminiChatRequest, got %T", info.Request), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	request, err := common.DeepCopy(geminiRequest)
	if err != nil {
		return nil, types.NewError(fmt.Errorf("failed to copy request to GeminiChatRequest: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	if err = helper.ModelMappedHelper(c, info, request); err != nil {
		return nil, types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	if model_setting.GetGeminiSettings().ThinkingAdapterEnabled {
		if isNoThinkingGeminiRequest(request) && !strings.Contains(info.OriginModelName, "-nothinking") {
			noThinkingModelName := info.OriginModelName + "-nothinking"
			if helper.HasModelBillingConfig(noThinkingModelName) {
				info.OriginModelName = noThinkingModelName
				info.UpstreamModelName = noThinkingModelName
			}
		}
		if request.GenerationConfig.ThinkingConfig == nil {
			relayconvert.ApplyGeminiThinkingConfig(request, info)
		}
	}

	adaptor, newAPIError := resolveTextAttemptAdaptor(info, adaptor)
	if newAPIError != nil {
		return nil, newAPIError
	}
	if newAPIError = validateAuthoritativeTextPreparation(info, adaptor, policy); newAPIError != nil {
		return nil, newAPIError
	}
	if info.ChannelSetting.SystemPrompt != "" {
		if request.SystemInstructions == nil {
			request.SystemInstructions = &dto.GeminiChatContent{Parts: []dto.GeminiPart{{Text: info.ChannelSetting.SystemPrompt}}}
		} else if len(request.SystemInstructions.Parts) == 0 {
			request.SystemInstructions.Parts = []dto.GeminiPart{{Text: info.ChannelSetting.SystemPrompt}}
		} else if info.ChannelSetting.SystemPromptOverride {
			common.SetContextKey(c, constant.ContextKeySystemPromptOverride, true)
			merged := false
			for index := range request.SystemInstructions.Parts {
				if request.SystemInstructions.Parts[index].Text == "" {
					continue
				}
				request.SystemInstructions.Parts[index].Text = info.ChannelSetting.SystemPrompt + "\n" + request.SystemInstructions.Parts[index].Text
				merged = true
				break
			}
			if !merged {
				request.SystemInstructions.Parts = append([]dto.GeminiPart{{Text: info.ChannelSetting.SystemPrompt}}, request.SystemInstructions.Parts...)
			}
		}
	}
	if request.SystemInstructions != nil {
		hasContent := false
		for _, part := range request.SystemInstructions.Parts {
			if part.Text != "" {
				hasContent = true
				break
			}
		}
		if !hasContent {
			request.SystemInstructions = nil
		}
	}

	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		storage, storageErr := common.GetBodyStorage(c)
		if storageErr != nil {
			return nil, types.NewErrorWithStatusCode(storageErr, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		preparedRequest, prepareErr := relaycommon.PrepareFinalPassThroughRelayRequest(info, storage)
		if prepareErr != nil {
			return nil, types.NewErrorWithStatusCode(prepareErr, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		return &PreparedTextRelayAttempt{
			info:               info,
			adaptor:            adaptor,
			preparedRequest:    preparedRequest,
			responseMode:       preparedTextResponseModeAdaptor,
			detectEventStream:  true,
			requestErrorPrefix: "Do gemini request failed: ",
			passThrough:        true,
		}, nil
	}

	convertedRequest, err := adaptor.ConvertGeminiRequest(c, info, request)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	return prepareConvertedTextAttempt(c, info, adaptor, convertedRequest, convertedTextAttemptOptions{
		removeDisabledFields: false,
		marshalErrorCode:     types.ErrorCodeConvertRequestFailed,
		logMessage:           "Gemini request body: %s",
		detectEventStream:    true,
		requestErrorPrefix:   "Do gemini request failed: ",
	}, policy)
}

// ExecutePreparedTextRelayAttempt sends the exact snapshot created during
// preparation and performs the matching response conversion.
func ExecutePreparedTextRelayAttempt(c *gin.Context, info *relaycommon.RelayInfo, attempt *PreparedTextRelayAttempt) (*dto.Usage, *types.NewAPIError) {
	if attempt == nil || attempt.preparedRequest == nil || attempt.adaptor == nil {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("prepared text relay attempt is required"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if info == nil || attempt.info != info {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("prepared text relay attempt does not belong to relay info"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	attempt.lifecycleMutex.Lock()
	if attempt.closed {
		attempt.lifecycleMutex.Unlock()
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("prepared text relay attempt is closed"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if attempt.executed {
		attempt.lifecycleMutex.Unlock()
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("prepared text relay attempt has already been executed"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	attempt.executed = true
	defer attempt.lifecycleMutex.Unlock()

	requestBody, err := attempt.preparedRequest.Reader()
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
	}
	if attempt.responseMode == preparedTextResponseModeChatViaResponses {
		return executePreparedChatCompletionsViaResponses(c, info, attempt.adaptor, requestBody)
	}
	if attempt.responseMode == preparedTextResponseModeResponsesViaChat {
		return executePreparedResponsesViaChat(c, info, attempt.adaptor, requestBody)
	}

	var httpResponse *http.Response
	response, err := attempt.adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		if attempt.requestErrorPrefix != "" {
			logger.LogError(c, attempt.requestErrorPrefix+err.Error())
		}
		return nil, types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}

	statusCodeMapping := c.GetString("status_code_mapping")
	if response != nil {
		httpResponse = response.(*http.Response)
		if attempt.detectEventStream {
			info.IsStream = info.IsStream || strings.HasPrefix(httpResponse.Header.Get("Content-Type"), "text/event-stream")
		}
		if httpResponse.StatusCode != http.StatusOK {
			newAPIError := service.RelayErrorHandler(c.Request.Context(), httpResponse, false)
			service.ResetStatusCode(newAPIError, statusCodeMapping)
			return nil, newAPIError
		}
	}

	usage, newAPIError := attempt.adaptor.DoResponse(c, httpResponse, info)
	if newAPIError != nil {
		service.ResetStatusCode(newAPIError, statusCodeMapping)
		return nil, newAPIError
	}
	return usage.(*dto.Usage), nil
}
