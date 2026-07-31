package middleware

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/service/providerfile"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
)

func managedProviderFileReferencesRequested(c *gin.Context) (bool, int, error) {
	if c == nil || c.Request == nil || c.Request.Method != http.MethodPost || c.Request.URL.Path != "/v1/responses" ||
		!strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		return false, 0, nil
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return false, http.StatusBadRequest, providerfile.ErrInvalidReference
	}
	body, err := storage.Bytes()
	if err != nil {
		return false, http.StatusBadRequest, providerfile.ErrInvalidReference
	}
	hasReferences, err := providerfile.HasManagedReferences(body)
	if err != nil {
		return false, http.StatusBadRequest, err
	}
	return hasReferences, 0, nil
}

func prepareManagedProviderFileResolution(c *gin.Context, modelName string) (*providerfile.Resolution, int, error) {
	hasReferences, status, err := managedProviderFileReferencesRequested(c)
	if err != nil || !hasReferences {
		return nil, status, err
	}
	if strings.TrimSpace(modelName) == "" || strings.HasPrefix(modelName, "auto:") || strings.HasPrefix(modelName, "smart:") {
		return nil, http.StatusBadRequest, fmt.Errorf("managed provider file model is unsupported")
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, http.StatusBadRequest, providerfile.ErrInvalidReference
	}
	body, err := storage.Bytes()
	if err != nil {
		return nil, http.StatusBadRequest, providerfile.ErrInvalidReference
	}
	if len(body) == 0 {
		return nil, 0, nil
	}
	common.SetContextKey(c, constant.ContextKeySuppressDebugLog, true)
	settings := model_setting.GetSmartRoutingSettings()
	runtime, err := contextconsensus.NewManagedConsensusCryptoRuntimeFromEnvironment()
	if err != nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("managed provider file lifecycle is unavailable")
	}
	target, err := providerfile.LoadTarget(settings, runtime, nil)
	if err != nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("managed provider file target is unavailable")
	}
	if _, err := selectManagedProviderFileGroup(c, target.ChannelID, modelName, common.GetContextKeyString(c, constant.ContextKeyUsingGroup)); err != nil {
		return nil, http.StatusForbidden, fmt.Errorf("managed provider file target is not authorized")
	}
	resolution, rewrittenBody, err := providerfile.PrepareResolution(c.Request.Context(), body,
		providerfile.NewOwner(common.GetContextKeyInt(c, constant.ContextKeyUserId), common.GetContextKeyInt(c, constant.ContextKeyTokenId)), runtime, target)
	if err != nil {
		return nil, http.StatusConflict, fmt.Errorf("managed provider file binding is invalid")
	}
	newStorage, err := common.CreateBodyStorage(rewrittenBody)
	if err != nil {
		return nil, http.StatusServiceUnavailable, fmt.Errorf("managed provider file request body is unavailable")
	}
	c.Set(common.KeyBodyStorage, newStorage)
	c.Set(common.KeyRequestBody, append([]byte(nil), rewrittenBody...))
	c.Request.Body = io.NopCloser(newStorage)
	c.Request.ContentLength = int64(len(rewrittenBody))
	c.Request.Header.Set("Content-Length", strconv.Itoa(len(rewrittenBody)))
	if storage != newStorage {
		_ = storage.Close()
	}
	common.SetContextKey(c, constant.ContextKeyManagedProviderFiles, resolution)
	if policy, found := common.GetContextKeyType[contextconsensus.CompactionPolicySnapshot](c, constant.ContextKeyContextConsensusPolicy); found {
		policy.SystemEnabled = false
		common.SetContextKey(c, constant.ContextKeyContextConsensusPolicy, policy)
	}
	return resolution, 0, nil
}
