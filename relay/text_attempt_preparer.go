package relay

import (
	"encoding/json"
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
	restoreProtocolMode bool
	savedRelayMode      int
	savedRequestURLPath string
	lifecycleMutex      sync.Mutex
	executed            bool
	closed              bool
	closeOnce           sync.Once
	closeErr            error
}

// PreparedRequest returns the immutable upstream request snapshot that will be
// consumed by ExecutePreparedTextRelayAttempt.
func (attempt *PreparedTextRelayAttempt) PreparedRequest() *relaycommon.PreparedRelayRequest {
	if attempt == nil {
		return nil
	}
	return attempt.preparedRequest
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
		return prepareOpenAITextAttemptWithAdaptor(c, info, nil)
	case types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIResponsesCompaction:
		return prepareResponsesTextAttemptWithAdaptor(c, info, nil)
	case types.RelayFormatClaude:
		return prepareClaudeTextAttemptWithAdaptor(c, info, nil)
	case types.RelayFormatGemini:
		return prepareGeminiTextAttemptWithAdaptor(c, info, nil)
	default:
		panic("validated text relay format was not dispatched")
	}
}

func prepareOpenAITextAttemptWithAdaptor(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor) (*PreparedTextRelayAttempt, *types.NewAPIError) {
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

	passThroughGlobal := model_setting.GetGlobalSettings().PassThroughRequestEnabled
	if info.RelayMode == relayconstant.RelayModeChatCompletions &&
		!passThroughGlobal &&
		!info.ChannelSetting.PassThroughBodyEnabled &&
		service.ShouldChatCompletionsUseResponsesGlobal(info.ChannelId, info.ChannelType, info.OriginModelName) {
		applySystemPromptIfNeeded(c, info, request)
		return prepareChatCompletionsViaResponsesAttempt(c, info, adaptor, request)
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
	}

	return &PreparedTextRelayAttempt{
		info:              info,
		adaptor:           adaptor,
		preparedRequest:   preparedRequest,
		responseMode:      preparedTextResponseModeAdaptor,
		detectEventStream: true,
	}, nil
}

type convertedTextAttemptOptions struct {
	removeDisabledFields bool
	marshalErrorCode     types.ErrorCode
	logMessage           string
	detectEventStream    bool
	requestErrorPrefix   string
}

func prepareConvertedTextAttempt(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, convertedRequest any, options convertedTextAttemptOptions) (*PreparedTextRelayAttempt, *types.NewAPIError) {
	relaycommon.AppendRequestConversionFromRequest(info, convertedRequest)
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
	return &PreparedTextRelayAttempt{
		info:               info,
		adaptor:            adaptor,
		preparedRequest:    preparedRequest,
		responseMode:       preparedTextResponseModeAdaptor,
		detectEventStream:  options.detectEventStream,
		requestErrorPrefix: options.requestErrorPrefix,
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
	if info.RelayMode == relayconstant.RelayModeResponsesCompact &&
		!common.IsResponsesCompactAPIType(info.ApiType) {
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
		}, nil
	}

	convertedRequest, err := adaptor.ConvertOpenAIResponsesRequest(c, info, *request)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	return prepareConvertedTextAttempt(c, info, adaptor, convertedRequest, convertedTextAttemptOptions{
		removeDisabledFields: true,
		marshalErrorCode:     types.ErrorCodeConvertRequestFailed,
		logMessage:           "requestBody: %s",
		detectEventStream:    false,
	})
}

func prepareClaudeTextAttemptWithAdaptor(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor) (*PreparedTextRelayAttempt, *types.NewAPIError) {
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
		return prepareChatCompletionsViaResponsesAttempt(c, info, adaptor, openAIRequest)
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
	})
}

func isNoThinkingGeminiRequest(request *dto.GeminiChatRequest) bool {
	if request.GenerationConfig.ThinkingConfig == nil || request.GenerationConfig.ThinkingConfig.ThinkingBudget == nil {
		return false
	}
	return *request.GenerationConfig.ThinkingConfig.ThinkingBudget == 0
}

func prepareGeminiTextAttemptWithAdaptor(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor) (*PreparedTextRelayAttempt, *types.NewAPIError) {
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
	})
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
