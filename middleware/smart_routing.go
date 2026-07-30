package middleware

import (
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/service/smartrouting"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type smartRoutingSelection struct {
	channel   *model.Channel
	group     string
	modelName string
}

type smartRoutingContextValidationError struct {
	cause error
}

func (validationError *smartRoutingContextValidationError) Error() string {
	return validationError.cause.Error()
}

type smartRoutingContextBindingError struct {
	reasonCodes []string
}

func (bindingError *smartRoutingContextBindingError) Error() string {
	return "smart routing cannot resolve provider-bound context state: " + strings.Join(bindingError.reasonCodes, ", ")
}

func resolveSmartRoutingSelection(c *gin.Context, modelRequest *ModelRequest, usingGroup string) (*smartRoutingSelection, bool, error) {
	if c == nil || modelRequest == nil {
		return nil, false, nil
	}
	profile, ok := smartrouting.ResolveVirtualModel(modelRequest.Model)
	if !ok {
		return nil, false, nil
	}

	routeRequest, err := buildSmartRouteRequest(c, modelRequest, usingGroup)
	if err != nil {
		return nil, true, err
	}
	if routeRequest.ContextConstraint.WouldBlock {
		return nil, true, &smartRoutingContextBindingError{reasonCodes: routeRequest.ContextConstraint.ReasonCodes}
	}
	analysis := smartrouting.AnalyzeRequest(routeRequest)
	candidates, err := buildSmartRouteCandidates(c, routeRequest, usingGroup)
	if err != nil {
		return nil, true, err
	}
	candidates, configuredPoolApplied := filterSmartRoutingCandidatesByConfiguredModelPool(candidates, profile.Name)
	if configuredPoolApplied && len(candidates) == 0 {
		return nil, true, fmt.Errorf("no smart routing candidate for virtual model %s after applying configured model pool", modelRequest.Model)
	}
	candidates = applySmartRoutingAffinity(c, candidates)
	authoritativeCandidates, _ := smartrouting.RankCandidatesForAuthoritativeValidation(routeRequest, candidates, profile.Policy)
	authoritativeCandidates = filterSmartRoutingCandidatesByTokenLimit(c, authoritativeCandidates)
	authoritativeCandidates = stabilizeSmartRoutingSession(authoritativeCandidates)
	freezeSmartRoutingCandidates(c, authoritativeCandidates)
	ranked, _ := smartrouting.RankCandidates(routeRequest, candidates, profile.Policy)
	ranked = filterSmartRoutingCandidatesByTokenLimit(c, ranked)
	ranked = stabilizeSmartRoutingSession(ranked)
	if len(ranked) == 0 && contextConsensusPreparationEnabled(c) {
		ranked = append([]smartrouting.SmartRouteCandidate(nil), authoritativeCandidates...)
	}
	if len(ranked) == 0 {
		return nil, true, fmt.Errorf("no smart routing candidate for virtual model %s", modelRequest.Model)
	}

	selected := ranked[0]
	channel, err := getSmartRoutingChannel(selected.ChannelID)
	if err != nil {
		return nil, true, err
	}
	if channel == nil {
		return nil, true, fmt.Errorf("smart routing selected missing channel %d", selected.ChannelID)
	}
	if channel.Status != common.ChannelStatusEnabled {
		return nil, true, fmt.Errorf("smart routing selected disabled channel %d", selected.ChannelID)
	}
	if !channelSupportsRequestPath(channel, c.Request.URL.Path, selected.ModelName) {
		return nil, true, fmt.Errorf("smart routing selected channel %d does not support request path", selected.ChannelID)
	}
	bindSmartRoutingAffinity(c, selected)
	if err := rewriteJSONRequestModel(c, selected.ModelName); err != nil {
		return nil, true, err
	}
	setSmartRoutingRetryCandidates(c, ranked, selected.ModelName, smartRoutingContextSwitchAllowed(routeRequest))

	common.SetContextKey(c, constant.ContextKeySmartRoutingDecision, smartrouting.Decision{
		Enabled:            true,
		Policy:             profile.Policy,
		TaskComplexity:     analysis.TaskComplexity,
		TaskType:           analysis.TaskType,
		RecommendedTier:    analysis.RecommendedTier,
		ContextRequirement: analysis.ContextRequirement,
		OriginalModel:      modelRequest.Model,
		SelectedModel:      selected.ModelName,
		SelectedChannelID:  selected.ChannelID,
		SelectedHealth:     selected.HealthState,
		CandidateCount:     len(ranked),
		FallbackIndex:      0,
		ScoreFactors:       selected.ScoreFactors,
		DecisionReasons:    smartRoutingDecisionReasons(analysis),
		ContextConsensus:   smartRoutingContextConsensusLog(routeRequest),
	})

	return &smartRoutingSelection{
		channel:   channel,
		group:     selected.Group,
		modelName: selected.ModelName,
	}, true, nil
}

func resolveExplicitModelChannelSelection(c *gin.Context, modelRequest *ModelRequest, usingGroup string) (*smartRoutingSelection, bool, error) {
	if c == nil || modelRequest == nil {
		return nil, false, nil
	}
	if _, ok := smartrouting.ResolveVirtualModel(modelRequest.Model); ok {
		return nil, false, nil
	}

	routeRequest, err := buildSmartRouteRequest(c, modelRequest, usingGroup)
	if err != nil {
		return nil, true, err
	}
	analysis := smartrouting.AnalyzeRequest(routeRequest)
	candidates, err := buildSmartRouteCandidates(c, routeRequest, usingGroup)
	if err != nil {
		return nil, false, nil
	}
	candidates = filterSmartRoutingCandidatesByRequestedModel(candidates, modelRequest.Model)
	if len(candidates) == 0 {
		return nil, false, nil
	}
	candidates = applySmartRoutingAffinity(c, candidates)
	authoritativeCandidates, _ := smartrouting.RankCandidatesForAuthoritativeValidation(routeRequest, candidates, smartrouting.PolicyBalanced)
	authoritativeCandidates = stabilizeSmartRoutingSession(authoritativeCandidates)
	freezeSmartRoutingCandidates(c, authoritativeCandidates)

	ranked, _ := smartrouting.RankCandidates(routeRequest, candidates, smartrouting.PolicyBalanced)
	ranked = stabilizeSmartRoutingSession(ranked)
	if len(ranked) == 0 && contextConsensusPreparationEnabled(c) {
		ranked = append([]smartrouting.SmartRouteCandidate(nil), authoritativeCandidates...)
	}
	if len(ranked) == 0 {
		return nil, false, nil
	}
	selected := ranked[0]
	channel, err := getSmartRoutingChannel(selected.ChannelID)
	if err != nil || channel == nil || channel.Status != common.ChannelStatusEnabled || !channelSupportsRequestPath(channel, c.Request.URL.Path, modelRequest.Model) {
		return nil, false, nil
	}
	bindSmartRoutingAffinity(c, selected)
	setSmartRoutingRetryCandidates(c, ranked, selected.ModelName, smartRoutingContextSwitchAllowed(routeRequest))

	common.SetContextKey(c, constant.ContextKeySmartRoutingDecision, smartrouting.Decision{
		Enabled:            true,
		Policy:             smartrouting.PolicyBalanced,
		TaskComplexity:     analysis.TaskComplexity,
		TaskType:           analysis.TaskType,
		RecommendedTier:    analysis.RecommendedTier,
		ContextRequirement: analysis.ContextRequirement,
		OriginalModel:      modelRequest.Model,
		SelectedModel:      modelRequest.Model,
		SelectedChannelID:  selected.ChannelID,
		SelectedHealth:     selected.HealthState,
		CandidateCount:     len(ranked),
		FallbackIndex:      0,
		ScoreFactors:       selected.ScoreFactors,
		DecisionReasons:    smartRoutingDecisionReasons(analysis),
		ContextConsensus:   smartRoutingContextConsensusLog(routeRequest),
	})

	return &smartRoutingSelection{
		channel:   channel,
		group:     selected.Group,
		modelName: modelRequest.Model,
	}, true, nil
}

func buildSmartRouteRequest(c *gin.Context, modelRequest *ModelRequest, usingGroup string) (smartrouting.SmartRouteRequest, error) {
	request := smartrouting.SmartRouteRequest{
		OriginalModel: modelRequest.Model,
		EndpointType:  smartRoutingEndpoint(c),
		UsingGroup:    usingGroup,
		TokenID:       common.GetContextKeyInt(c, constant.ContextKeyTokenId),
		UserID:        common.GetContextKeyInt(c, constant.ContextKeyUserId),
	}

	if !strings.HasPrefix(c.Request.Header.Get("Content-Type"), "application/json") {
		return request, nil
	}

	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return request, err
	}
	body, err := storage.Bytes()
	if err != nil {
		return request, err
	}
	if _, seekErr := storage.Seek(0, io.SeekStart); seekErr != nil {
		return request, seekErr
	}
	c.Request.Body = io.NopCloser(storage)

	request.Stream = gjson.GetBytes(body, "stream").Bool()
	request.MaxOutputTokens = maxGJSONInt(
		gjson.GetBytes(body, "max_tokens"),
		gjson.GetBytes(body, "max_completion_tokens"),
		gjson.GetBytes(body, "max_output_tokens"),
	)
	request.EstimatedPromptTokens = estimatePromptTokensFromBody(body)
	request.ContextTokensRequired = request.EstimatedPromptTokens + request.MaxOutputTokens

	toolsCount := maxGJSONInt(gjson.GetBytes(body, "tools.#"), gjson.GetBytes(body, "functions.#"))
	request.ToolCount = toolsCount
	request.HasTools = toolsCount > 0 || gjson.GetBytes(body, "tool_choice").Exists()
	request.RequiresJSONSchema = smartRoutingHasJSONSchema(body)
	request.HasImages = smartRoutingBodyContains(body, "image_url", "input_image", "\"type\":\"image\"")
	request.HasAudio = smartRoutingBodyContains(body, "input_audio", "\"audio\"", "audio_url")
	request.HasFiles = smartRoutingBodyContains(body, "input_file", "file_id", "file_data", "\"type\":\"file\"")
	request.ReasoningRequested = gjson.GetBytes(body, "reasoning").Exists() ||
		gjson.GetBytes(body, "reasoning_effort").Exists() ||
		gjson.GetBytes(body, "think").Exists() ||
		gjson.GetBytes(body, "thinking").Exists()

	request.TokenMeta = &types.TokenCountMeta{
		CombineText:   smartRoutingTextPreview(body),
		ToolsCount:    toolsCount,
		MessagesCount: maxGJSONInt(gjson.GetBytes(body, "messages.#"), gjson.GetBytes(body, "input.#")),
		MaxTokens:     request.MaxOutputTokens,
		Files:         smartRoutingFileMeta(request),
	}
	if protocol, supported := smartRoutingContextProtocol(c); supported {
		envelope, extractErr := contextconsensus.Extract(contextconsensus.ExtractionRequest{
			Protocol:      protocol,
			OriginalModel: modelRequest.Model,
			Body:          body,
		})
		if envelope != nil {
			request.ContextConstraint = envelope.RoutingConstraint(request.EstimatedPromptTokens)
			toolContextPresent := len(envelope.ToolState.Exchanges) > 0 || envelope.ToolState.SchemaDigest != ""
			for _, segment := range envelope.PreservedSegments {
				if segment.Kind == contextconsensus.SegmentKindToolCall || segment.Kind == contextconsensus.SegmentKindToolResult {
					toolContextPresent = true
					break
				}
			}
			if toolContextPresent {
				common.SetContextKey(c, constant.ContextKeySuppressDebugLog, true)
			}
		}
		if extractErr != nil {
			return request, &smartRoutingContextValidationError{cause: extractErr}
		}
	}
	return request, nil
}

func buildSmartRouteCandidates(c *gin.Context, request smartrouting.SmartRouteRequest, usingGroup string) ([]smartrouting.SmartRouteCandidate, error) {
	snapshots, err := loadSmartRoutingChannelSnapshots(c.Request.URL.Path)
	if err != nil {
		return nil, err
	}
	pricing := model.GetPricing()
	groups := smartRoutingGroups(c, usingGroup)
	candidates := make([]smartrouting.SmartRouteCandidate, 0)
	for _, group := range groups {
		requestForGroup := request
		requestForGroup.UsingGroup = group
		candidates = append(candidates, smartrouting.BuildCandidatesFromSnapshots(requestForGroup, pricing, snapshots)...)
	}
	return candidates, nil
}

func applySmartRoutingAffinity(c *gin.Context, candidates []smartrouting.SmartRouteCandidate) []smartrouting.SmartRouteCandidate {
	type affinityScope struct {
		modelName string
		group     string
	}

	preferences := make(map[affinityScope]service.ChannelAffinityPreference)
	for index := range candidates {
		scope := affinityScope{modelName: candidates[index].ModelName, group: candidates[index].Group}
		preference, resolved := preferences[scope]
		if !resolved {
			preference = service.PeekChannelAffinityPreference(c, scope.modelName, scope.group)
			preferences[scope] = preference
		}
		if !preference.Found || preference.ChannelID != candidates[index].ChannelID {
			continue
		}
		if preference.Kind == service.ChannelAffinityCache {
			candidates[index].CacheAffinityScore = 1
			continue
		}
		candidates[index].AffinityScore = 1
	}
	return candidates
}

func bindSmartRoutingAffinity(c *gin.Context, selected smartrouting.SmartRouteCandidate) {
	preference := service.ResolveChannelAffinityPreference(c, selected.ModelName, selected.Group)
	if preference.Found && preference.ChannelID == selected.ChannelID {
		service.MarkChannelAffinityUsed(c, selected.Group, selected.ChannelID)
	}
}

func stabilizeSmartRoutingSession(candidates []smartrouting.SmartRouteCandidate) []smartrouting.SmartRouteCandidate {
	const maximumAffinityScoreGap = 0.1
	if len(candidates) < 2 {
		return candidates
	}
	selectedModel := candidates[0].ModelName
	for index := 1; index < len(candidates); index++ {
		candidate := candidates[index]
		if candidate.ModelName != selectedModel || candidate.HealthState != smartrouting.ChannelHealthHealthy ||
			(candidate.AffinityScore <= 0 && candidate.CacheAffinityScore <= 0) ||
			candidates[0].FinalScore-candidate.FinalScore > maximumAffinityScoreGap {
			continue
		}
		stableCandidate := candidate
		copy(candidates[1:index+1], candidates[0:index])
		candidates[0] = stableCandidate
		break
	}
	return candidates
}

func loadSmartRoutingChannelSnapshots(requestPath string) ([]smartrouting.ChannelSnapshot, error) {
	if model.DB == nil {
		return nil, errors.New("database is not initialized")
	}
	var channels []*model.Channel
	if err := model.DB.Omit("key").Find(&channels).Error; err != nil {
		return nil, err
	}
	snapshots := make([]smartrouting.ChannelSnapshot, 0, len(channels))
	for _, channel := range channels {
		if channel.Type == constant.ChannelTypeAdvancedCustom {
			config := channel.GetOtherSettings().AdvancedCustom
			if config == nil || !config.SupportsPath(requestPath) {
				continue
			}
		}
		snapshots = append(snapshots, smartrouting.NewChannelSnapshot(channel))
	}
	return snapshots, nil
}

func smartRoutingGroups(c *gin.Context, usingGroup string) []string {
	if usingGroup == "" {
		usingGroup = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	}
	if usingGroup != "auto" {
		return []string{usingGroup}
	}
	userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	return service.GetUserAutoGroup(userGroup)
}

func filterSmartRoutingCandidatesByTokenLimit(c *gin.Context, candidates []smartrouting.SmartRouteCandidate) []smartrouting.SmartRouteCandidate {
	if !common.GetContextKeyBool(c, constant.ContextKeyTokenModelLimitEnabled) {
		return candidates
	}
	rawLimit, ok := common.GetContextKey(c, constant.ContextKeyTokenModelLimit)
	if !ok {
		return nil
	}
	tokenModelLimit, ok := rawLimit.(map[string]bool)
	if !ok || len(tokenModelLimit) == 0 {
		return nil
	}
	filtered := make([]smartrouting.SmartRouteCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		matchName := ratio_setting.FormatMatchingModelName(candidate.ModelName)
		if tokenModelLimit[matchName] {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func filterSmartRoutingCandidatesByConfiguredModelPool(candidates []smartrouting.SmartRouteCandidate, virtualModel string) ([]smartrouting.SmartRouteCandidate, bool) {
	pool, configured := model_setting.GetSmartRoutingVirtualModelPool(virtualModel)
	if !configured {
		return candidates, false
	}

	allowedModels := make(map[string]struct{}, len(pool))
	for _, modelName := range pool {
		modelName = strings.TrimSpace(modelName)
		if modelName != "" {
			allowedModels[modelName] = struct{}{}
		}
	}
	if len(allowedModels) == 0 {
		return nil, true
	}

	filtered := make([]smartrouting.SmartRouteCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if _, ok := allowedModels[candidate.ModelName]; ok {
			filtered = append(filtered, candidate)
		}
	}
	return filtered, true
}

func filterSmartRoutingCandidatesByRequestedModel(candidates []smartrouting.SmartRouteCandidate, requestedModel string) []smartrouting.SmartRouteCandidate {
	if requestedModel == "" || len(candidates) == 0 {
		return nil
	}
	normalizedRequestedModel := ratio_setting.FormatMatchingModelName(requestedModel)
	filtered := make([]smartrouting.SmartRouteCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ModelName == requestedModel || candidate.ModelName == normalizedRequestedModel {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func setSmartRoutingRetryCandidates(c *gin.Context, candidates []smartrouting.SmartRouteCandidate, selectedModel string, switchAllowed bool) {
	if c == nil || selectedModel == "" || len(candidates) == 0 || !switchAllowed {
		return
	}
	retryCandidates := make([]smartrouting.SmartRouteCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ModelName == selectedModel {
			retryCandidates = append(retryCandidates, candidate)
		}
	}
	if len(retryCandidates) > 0 {
		common.SetContextKey(c, constant.ContextKeySmartRoutingRetryCandidates, retryCandidates)
	}
}

func freezeSmartRoutingCandidates(c *gin.Context, candidates []smartrouting.SmartRouteCandidate) {
	if !contextConsensusPreparationEnabled(c) || len(candidates) == 0 {
		return
	}
	common.SetContextKey(c, constant.ContextKeySmartRoutingFrozenCandidates, append([]smartrouting.SmartRouteCandidate(nil), candidates...))
}

func contextConsensusPreparationEnabled(c *gin.Context) bool {
	policy, ok := common.GetContextKeyType[contextconsensus.CompactionPolicySnapshot](c, constant.ContextKeyContextConsensusPolicy)
	return ok && policy.SystemEnabled
}

func smartRoutingContextProtocol(c *gin.Context) (types.RelayFormat, bool) {
	path := c.Request.URL.Path
	if strings.HasPrefix(path, "/v1/messages") {
		return types.RelayFormatClaude, true
	}
	switch relayconstant.Path2RelayMode(path) {
	case relayconstant.RelayModeResponses:
		return types.RelayFormatOpenAIResponses, true
	case relayconstant.RelayModeGemini:
		return types.RelayFormatGemini, true
	case relayconstant.RelayModeResponsesCompact:
		return "", false
	default:
		if smartRoutingEndpoint(c) == smartrouting.EndpointChatCompletions {
			return types.RelayFormatOpenAI, true
		}
		return "", false
	}
}

func smartRoutingContextConsensusLog(request smartrouting.SmartRouteRequest) smartrouting.ContextConsensusLog {
	constraint := request.ContextConstraint
	mode := constraint.Mode
	if mode == "" {
		mode = "stateless_full_context"
	}
	return smartrouting.ContextConsensusLog{
		Mode:                    mode,
		Version:                 1,
		ValidationMode:          constraint.ValidationMode,
		ValidationResult:        constraint.ValidationResult,
		Protocol:                string(constraint.Protocol),
		Compacted:               false,
		PreservedRecentMessages: preservedRecentMessages(request.TokenMeta),
		PreservedSegmentCount:   constraint.PreservedSegmentCount,
		ToolExchangeCount:       constraint.ToolExchangeCount,
		InputTokensBefore:       constraint.EffectivePromptTokens,
		BindingLevel:            string(constraint.RequiredBinding),
		BindingReasonCodes:      append([]string(nil), constraint.ReasonCodes...),
		SwitchAllowed:           smartRoutingContextSwitchAllowed(request),
		WouldBlock:              constraint.WouldBlock,
	}
}

func smartRoutingContextSwitchAllowed(request smartrouting.SmartRouteRequest) bool {
	return request.ContextConstraint.ValidationMode == "" || request.ContextConstraint.SwitchAllowed
}

func getSmartRoutingChannel(channelID int) (*model.Channel, error) {
	if common.MemoryCacheEnabled {
		return model.CacheGetChannel(channelID)
	}
	return model.GetChannelById(channelID, true)
}

func rewriteJSONRequestModel(c *gin.Context, modelName string) error {
	if !strings.HasPrefix(c.Request.Header.Get("Content-Type"), "application/json") {
		return nil
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return err
	}
	body, err := storage.Bytes()
	if err != nil {
		return err
	}
	var data map[string]interface{}
	if err := common.Unmarshal(body, &data); err != nil {
		return err
	}
	data["model"] = modelName
	jsonData, err := common.Marshal(data)
	if err != nil {
		return err
	}
	newStorage, err := common.CreateBodyStorage(jsonData)
	if err != nil {
		return err
	}
	_ = storage.Close()
	c.Set(common.KeyBodyStorage, newStorage)
	c.Set(common.KeyRequestBody, jsonData)
	c.Request.Body = io.NopCloser(newStorage)
	c.Request.ContentLength = int64(len(jsonData))
	c.Request.Header.Set("Content-Length", strconv.Itoa(len(jsonData)))
	return nil
}

func smartRoutingEndpoint(c *gin.Context) smartrouting.EndpointType {
	path := c.Request.URL.Path
	if strings.HasPrefix(path, "/v1/messages") {
		return smartrouting.EndpointClaudeMessages
	}
	switch relayconstant.Path2RelayMode(path) {
	case relayconstant.RelayModeResponses, relayconstant.RelayModeResponsesCompact:
		return smartrouting.EndpointResponses
	case relayconstant.RelayModeGemini:
		return smartrouting.EndpointGemini
	case relayconstant.RelayModeEmbeddings:
		return smartrouting.EndpointEmbedding
	case relayconstant.RelayModeRerank:
		return smartrouting.EndpointRerank
	case relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits:
		return smartrouting.EndpointImage
	case relayconstant.RelayModeAudioSpeech, relayconstant.RelayModeAudioTranscription, relayconstant.RelayModeAudioTranslation:
		return smartrouting.EndpointAudio
	default:
		return smartrouting.EndpointChatCompletions
	}
}

func smartRoutingHasJSONSchema(body []byte) bool {
	if gjson.GetBytes(body, "response_format.type").String() == "json_schema" {
		return true
	}
	if gjson.GetBytes(body, "response_format.json_schema").Exists() {
		return true
	}
	if gjson.GetBytes(body, "text.format.type").String() == "json_schema" {
		return true
	}
	return false
}

func smartRoutingBodyContains(body []byte, terms ...string) bool {
	text := strings.ToLower(string(body))
	for _, term := range terms {
		if strings.Contains(text, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func smartRoutingTextPreview(body []byte) string {
	const maxPreviewBytes = 16384
	if len(body) <= maxPreviewBytes {
		return string(body)
	}
	return string(body[:maxPreviewBytes])
}

func smartRoutingFileMeta(request smartrouting.SmartRouteRequest) []*types.FileMeta {
	files := make([]*types.FileMeta, 0, 3)
	if request.HasImages {
		files = append(files, &types.FileMeta{FileType: types.FileTypeImage})
	}
	if request.HasAudio {
		files = append(files, &types.FileMeta{FileType: types.FileTypeAudio})
	}
	if request.HasFiles {
		files = append(files, &types.FileMeta{FileType: types.FileTypeFile})
	}
	return files
}

func smartRoutingDecisionReasons(analysis smartrouting.SmartRouteAnalysis) []string {
	reasons := make([]string, 0, len(analysis.TaskReasons)+len(analysis.ContextReasons))
	reasons = append(reasons, analysis.TaskReasons...)
	reasons = append(reasons, analysis.ContextReasons...)
	return reasons
}

func preservedRecentMessages(meta *types.TokenCountMeta) int {
	if meta == nil || meta.MessagesCount <= 0 {
		return 0
	}
	if meta.MessagesCount < 6 {
		return meta.MessagesCount
	}
	return 6
}

func estimatePromptTokensFromBody(body []byte) int {
	if len(body) == 0 {
		return 0
	}
	return int(math.Ceil(float64(len(body)) / 4))
}

func maxGJSONInt(values ...gjson.Result) int {
	maxValue := 0
	for _, value := range values {
		if !value.Exists() {
			continue
		}
		intValue := int(value.Int())
		if intValue > maxValue {
			maxValue = intValue
		}
	}
	return maxValue
}

func abortSmartRoutingError(c *gin.Context, usingGroup string, modelName string, err error) {
	var contextValidationError *smartRoutingContextValidationError
	if errors.As(err, &contextValidationError) {
		abortWithOpenAiMessage(c, http.StatusBadRequest, contextValidationError.Error(), types.ErrorCodeInvalidRequest)
		return
	}
	var contextBindingError *smartRoutingContextBindingError
	if errors.As(err, &contextBindingError) {
		abortWithOpenAiMessage(c, http.StatusConflict, contextBindingError.Error(), types.ErrorCodeInvalidRequest)
		return
	}
	showGroup := usingGroup
	if usingGroup == "auto" {
		if autoGroup := common.GetContextKeyString(c, constant.ContextKeyAutoGroup); autoGroup != "" {
			showGroup = fmt.Sprintf("auto(%s)", autoGroup)
		}
	}
	message := i18n.T(c, i18n.MsgDistributorGetChannelFailed, map[string]any{"Group": showGroup, "Model": modelName, "Error": err.Error()})
	abortWithOpenAiMessage(c, http.StatusServiceUnavailable, message, types.ErrorCodeModelNotFound)
}
