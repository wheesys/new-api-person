package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/service/providerfile"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
)

const managedProviderFileIdempotencyHeader = "X-New-Api-File-Idempotency-Key"

func UploadManagedProviderFile(c *gin.Context) {
	common.SetContextKey(c, constant.ContextKeySuppressDebugLog, true)
	idempotencyValues := c.Request.Header.Values(managedProviderFileIdempotencyHeader)
	c.Request.Header.Del(managedProviderFileIdempotencyHeader)
	if len(idempotencyValues) != 1 || !validManagedProviderFileIdempotencyKey(idempotencyValues[0]) {
		providerFileError(c, http.StatusBadRequest, "a valid provider file idempotency key is required", "invalid_idempotency_key")
		return
	}
	settings := model_setting.GetSmartRoutingSettings()
	runtime, err := contextconsensus.NewManagedConsensusCryptoRuntimeFromEnvironment()
	if err != nil {
		providerFileError(c, http.StatusServiceUnavailable, "managed provider file lifecycle is unavailable", "provider_file_unavailable")
		return
	}
	target, err := providerfile.LoadTarget(settings, runtime, nil)
	if err != nil {
		providerFileError(c, http.StatusServiceUnavailable, "managed provider file target is unavailable", "provider_file_target_unavailable")
		return
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		providerFileError(c, http.StatusBadRequest, "provider file upload body is invalid", "invalid_upload_body")
		return
	}
	uploadBody, err := providerfile.ParseUploadBody(storage, c.GetHeader("Content-Type"))
	if err != nil {
		providerFileError(c, http.StatusBadRequest, "provider file upload body is invalid", "invalid_upload_body")
		return
	}
	file, err := providerfile.Upload(c.Request.Context(), providerfile.UploadRequest{
		Owner:          providerfile.NewOwner(common.GetContextKeyInt(c, constant.ContextKeyUserId), common.GetContextKeyInt(c, constant.ContextKeyTokenId)),
		IdempotencyKey: idempotencyValues[0], Body: uploadBody, Settings: settings, Runtime: runtime, Target: target,
	})
	if err != nil {
		providerFileLifecycleError(c, err)
		return
	}
	c.JSON(http.StatusOK, file)
}

func RetrieveManagedProviderFile(c *gin.Context) {
	common.SetContextKey(c, constant.ContextKeySuppressDebugLog, true)
	settings := model_setting.GetSmartRoutingSettings()
	runtime, err := contextconsensus.NewManagedConsensusCryptoRuntimeFromEnvironment()
	if err != nil {
		providerFileError(c, http.StatusServiceUnavailable, "managed provider file lifecycle is unavailable", "provider_file_unavailable")
		return
	}
	target, err := providerfile.LoadTarget(settings, runtime, nil)
	if err != nil {
		providerFileError(c, http.StatusServiceUnavailable, "managed provider file target is unavailable", "provider_file_target_unavailable")
		return
	}
	file, err := providerfile.Retrieve(c.Request.Context(), providerfile.RetrieveRequest{
		Owner:  providerfile.NewOwner(common.GetContextKeyInt(c, constant.ContextKeyUserId), common.GetContextKeyInt(c, constant.ContextKeyTokenId)),
		Handle: c.Param("id"), Runtime: runtime, Target: target,
	})
	if err != nil {
		providerFileLifecycleError(c, err)
		return
	}
	c.JSON(http.StatusOK, file)
}

func providerFileLifecycleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, providerfile.ErrLifecycleConflict):
		providerFileError(c, http.StatusConflict, "managed provider file request conflicts with its lifecycle", "provider_file_conflict")
	case errors.Is(err, providerfile.ErrUploadOutcomeUnknown):
		providerFileError(c, http.StatusServiceUnavailable, "managed provider file upload outcome is unknown", "provider_file_upload_unknown")
	default:
		providerFileError(c, http.StatusServiceUnavailable, "managed provider file lifecycle is unavailable", "provider_file_unavailable")
	}
}

func providerFileError(c *gin.Context, status int, message, code string) {
	c.JSON(status, gin.H{"error": types.OpenAIError{
		Message: common.MessageWithRequestId(message, c.GetString(common.RequestIdKey)), Type: "new_api_error", Code: code,
	}})
}

func validManagedProviderFileIdempotencyKey(value string) bool {
	if len(value) < 16 || len(value) > 128 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || character == '.' || character == '_' || character == '~' || character == '-' {
			continue
		}
		return false
	}
	return true
}
