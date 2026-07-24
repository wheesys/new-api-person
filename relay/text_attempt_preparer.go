package relay

import (
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
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
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

// PrepareTextRelayAttempt prepares one OpenAI-format text attempt without
// performing network I/O or response handling.
func PrepareTextRelayAttempt(c *gin.Context, info *relaycommon.RelayInfo) (*PreparedTextRelayAttempt, *types.NewAPIError) {
	if info == nil {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("relay info is required"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	if info.RelayFormat != types.RelayFormatOpenAI {
		return nil, types.NewErrorWithStatusCode(fmt.Errorf("unsupported text relay format: %s", info.RelayFormat), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	info.InitChannelMeta(c)
	info.ResetPreparedRelayRequest()

	return prepareOpenAITextAttemptWithAdaptor(c, info, nil)
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
		info:            info,
		adaptor:         adaptor,
		preparedRequest: preparedRequest,
		responseMode:    preparedTextResponseModeAdaptor,
	}, nil
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
		return nil, types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}

	statusCodeMapping := c.GetString("status_code_mapping")
	if response != nil {
		httpResponse = response.(*http.Response)
		info.IsStream = info.IsStream || strings.HasPrefix(httpResponse.Header.Get("Content-Type"), "text/event-stream")
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
