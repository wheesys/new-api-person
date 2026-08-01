package providerfile

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTargetFromChannelAllowsOptionalProject(t *testing.T) {
	client := &http.Client{Transport: http.DefaultTransport}
	channel := &model.Channel{
		Id: 41, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Key: "sk-safe",
	}
	target, err := targetFromChannel(channel, client, true)
	require.NoError(t, err)
	assert.Equal(t, 41, target.ChannelID)
	assert.Equal(t, "https://api.openai.com", target.Endpoint)
	assert.Empty(t, target.Project)

	channel.OpenAIProject = common.GetPointer("proj-provider-file")
	target, err = targetFromChannel(channel, client, true)
	require.NoError(t, err)
	assert.Equal(t, "proj-provider-file", target.Project)
	assert.NotContains(t, target.String(), channel.Key)
}

func TestTargetFromChannelRejectsUnsupportedChannelConfiguration(t *testing.T) {
	client := &http.Client{Transport: http.DefaultTransport}
	channel := &model.Channel{
		Id: 41, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Key: "sk-safe",
	}

	customEndpoint := "https://compatible.example"
	channel.BaseURL = &customEndpoint
	_, err := targetFromChannel(channel, client, true)
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
	channel.HeaderOverride = nil

	channel.OpenAIProject = common.GetPointer(" project-with-space")
	_, err = targetFromChannel(channel, client, true)
	assert.ErrorIs(t, err, ErrTargetUnavailable)
}

func TestLoadTargetUsesEnabledConfiguredChannel(t *testing.T) {
	runtime := prepareProviderFileLifecycleTest(t)
	settings := providerFileLifecycleTestSettings()

	target, err := LoadTarget(settings, runtime, nil)
	require.NoError(t, err)
	assert.Equal(t, "proj-provider-file", target.Project)

	settings.ProviderFileLifecycleEnabled = false
	_, err = LoadTarget(settings, runtime, nil)
	assert.ErrorIs(t, err, ErrTargetUnavailable)
	settings.ProviderFileLifecycleEnabled = true

	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", 41).Update("OpenAIProject", nil).Error)
	target, err = LoadTarget(settings, runtime, nil)
	require.NoError(t, err)
	assert.Empty(t, target.Project)
}
