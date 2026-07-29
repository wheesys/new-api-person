package relay

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
)

func trimModelThinking(modelName string) string {
	// 去除模型名称中的 -nothinking 后缀
	if strings.HasSuffix(modelName, "-nothinking") {
		return strings.TrimSuffix(modelName, "-nothinking")
	}
	// 去除模型名称中的 -thinking 后缀
	if strings.HasSuffix(modelName, "-thinking") {
		return strings.TrimSuffix(modelName, "-thinking")
	}

	// 去除模型名称中的 -thinking-number
	if strings.Contains(modelName, "-thinking-") {
		parts := strings.Split(modelName, "-thinking-")
		if len(parts) > 1 {
			return parts[0] + "-thinking"
		}
	}
	return modelName
}

func GeminiHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	_, newAPIError = ExecuteTextAttemptWithSettlement(c, info)
	return newAPIError
}

func GeminiEmbeddingHandler(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)

	isBatch := strings.HasSuffix(c.Request.URL.Path, "batchEmbedContents")
	info.IsGeminiBatchEmbedding = isBatch

	var req dto.Request
	var err error
	var inputTexts []string

	if isBatch {
		batchRequest := &dto.GeminiBatchEmbeddingRequest{}
		err = common.UnmarshalBodyReusable(c, batchRequest)
		if err != nil {
			return types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		req = batchRequest
		for _, r := range batchRequest.Requests {
			for _, part := range r.Content.Parts {
				if part.Text != "" {
					inputTexts = append(inputTexts, part.Text)
				}
			}
		}
	} else {
		singleRequest := &dto.GeminiEmbeddingRequest{}
		err = common.UnmarshalBodyReusable(c, singleRequest)
		if err != nil {
			return types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
		}
		req = singleRequest
		for _, part := range singleRequest.Content.Parts {
			if part.Text != "" {
				inputTexts = append(inputTexts, part.Text)
			}
		}
	}

	err = helper.ModelMappedHelper(c, info, req)
	if err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	req.SetModelName("models/" + info.UpstreamModelName)

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)

	var requestBody io.Reader
	jsonData, err := common.Marshal(req)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}

	// apply param override
	if len(info.ParamOverride) > 0 {
		jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
		if err != nil {
			return newAPIErrorFromParamOverride(err)
		}
	}
	logger.LogDebug(c, "Gemini embedding request body: %s", jsonData)
	body, size, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	defer closer.Close()
	jsonData = nil
	info.UpstreamRequestBodySize = size
	requestBody = body

	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		logger.LogError(c, "Do gemini request failed: "+err.Error())
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")
	var httpResp *http.Response
	if resp != nil {
		httpResp = resp.(*http.Response)
		if httpResp.StatusCode != http.StatusOK {
			newAPIError = service.RelayErrorHandler(c.Request.Context(), httpResp, false)
			service.ResetStatusCode(newAPIError, statusCodeMappingStr)
			return newAPIError
		}
	}

	usage, openaiErr := adaptor.DoResponse(c, resp.(*http.Response), info)
	if openaiErr != nil {
		service.ResetStatusCode(openaiErr, statusCodeMappingStr)
		return openaiErr
	}

	service.PostTextConsumeQuota(c, info, usage.(*dto.Usage), nil)
	return nil
}
