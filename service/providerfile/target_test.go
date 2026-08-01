package providerfile

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTargetFromChannelRequiresDedicatedOfficialSingleKeyChannel(t *testing.T) {
	client := &http.Client{Transport: http.DefaultTransport}
	channel := &model.Channel{
		Id: 41, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Key: "sk-safe",
		OpenAIProject: common.GetPointer("proj-exclusive"),
	}
	target, err := targetFromChannel(channel, client, true)
	require.NoError(t, err)
	assert.Equal(t, 41, target.ChannelID)
	assert.Equal(t, "https://api.openai.com", target.Endpoint)
	assert.Equal(t, "proj-exclusive", target.Project)
	assert.NotContains(t, target.String(), channel.Key)
	channel.OpenAIProject = nil
	_, err = targetFromChannel(channel, client, true)
	assert.ErrorIs(t, err, ErrTargetUnavailable)
	channel.OpenAIProject = common.GetPointer("proj-exclusive")

	customEndpoint := "https://compatible.example"
	channel.BaseURL = &customEndpoint
	_, err = targetFromChannel(channel, client, true)
	assert.ErrorIs(t, err, ErrTargetUnavailable)
	channel.BaseURL = nil
	channel.ChannelInfo.IsMultiKey = true
	_, err = targetFromChannel(channel, client, true)
	assert.ErrorIs(t, err, ErrTargetUnavailable)
	channel.ChannelInfo.IsMultiKey = false
	override := `{"Authorization":"forbidden"}`
	channel.HeaderOverride = &override
	_, err = targetFromChannel(channel, client, true)
	assert.ErrorIs(t, err, ErrTargetUnavailable)
}

func TestLoadTargetRequiresValidTargetBoundReadinessEvidence(t *testing.T) {
	runtime := prepareProviderFileLifecycleTest(t)
	settings := &model_setting.SmartRoutingSettings{
		ProviderFileLifecycleEnabled: true, ProviderFileOpenAIChannelID: 41, ProviderFileExpirationSeconds: 3600,
		ProviderFileMetadataVerifyTTLSeconds: 0, ProviderFileDeletionLeadSeconds: 60, ProviderFileDeletionBatchSize: 10,
		ProviderFileDeletionMaxAttempts: 3, ProviderFileDeletionTimeoutSeconds: 5,
		ProviderFileExclusiveProjectAttested: true, ProviderFileSandboxContractVerified: true,
	}
	target, err := LoadTarget(settings, runtime, nil)
	require.NoError(t, err)
	assert.Equal(t, "proj-exclusive", target.Project)

	settings.ProviderFileSandboxContractVerified = false
	_, err = LoadTarget(settings, runtime, nil)
	assert.ErrorIs(t, err, ErrTargetUnavailable)
	settings.ProviderFileSandboxContractVerified = true

	var evidence model.ManagedProviderFileReadinessEvidence
	require.NoError(t, model.DB.First(&evidence).Error)
	assert.ErrorIs(t, model.DB.WithContext(context.Background()).Model(&evidence).Update("expires_at", time.Now().UTC().Add(time.Hour)).Error,
		model.ErrManagedProviderFileReadinessEvidenceImmutable)
	require.NoError(t, model.DB.Exec("UPDATE managed_provider_file_readiness_evidences SET evidence_hmac = ? WHERE id = ?", strings.Repeat("0", 64), evidence.Id).Error)
	_, err = LoadTarget(settings, runtime, nil)
	assert.ErrorIs(t, err, ErrTargetUnavailable)
}

func TestLoadTargetRejectsDisabledUploadsAndCredentialRotationWithoutNewEvidence(t *testing.T) {
	runtime := prepareProviderFileLifecycleTest(t)
	settings := &model_setting.SmartRoutingSettings{
		ProviderFileLifecycleEnabled: true, ProviderFileOpenAIChannelID: 41, ProviderFileExpirationSeconds: 3600,
		ProviderFileMetadataVerifyTTLSeconds: 0, ProviderFileDeletionLeadSeconds: 60, ProviderFileDeletionBatchSize: 10,
		ProviderFileDeletionMaxAttempts: 3, ProviderFileDeletionTimeoutSeconds: 5,
		ProviderFileExclusiveProjectAttested: true, ProviderFileSandboxContractVerified: true,
	}

	settings.ProviderFileLifecycleEnabled = false
	_, err := LoadTarget(settings, runtime, nil)
	assert.ErrorIs(t, err, ErrTargetUnavailable)
	settings.ProviderFileLifecycleEnabled = true

	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", 41).Update("key", "sk-rotated-secret").Error)
	_, err = LoadTarget(settings, runtime, nil)
	assert.ErrorIs(t, err, ErrTargetUnavailable)
}
