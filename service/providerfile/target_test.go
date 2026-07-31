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

func TestTargetFromChannelRequiresDedicatedOfficialSingleKeyChannel(t *testing.T) {
	client := &http.Client{Transport: http.DefaultTransport}
	channel := &model.Channel{Id: 41, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Key: "sk-safe"}
	target, err := targetFromChannel(channel, client)
	require.NoError(t, err)
	assert.Equal(t, 41, target.ChannelID)
	assert.Equal(t, "https://api.openai.com", target.Endpoint)
	assert.NotContains(t, target.String(), channel.Key)

	customEndpoint := "https://compatible.example"
	channel.BaseURL = &customEndpoint
	_, err = targetFromChannel(channel, client)
	assert.ErrorIs(t, err, ErrTargetUnavailable)
	channel.BaseURL = nil
	channel.ChannelInfo.IsMultiKey = true
	_, err = targetFromChannel(channel, client)
	assert.ErrorIs(t, err, ErrTargetUnavailable)
	channel.ChannelInfo.IsMultiKey = false
	override := `{"Authorization":"forbidden"}`
	channel.HeaderOverride = &override
	_, err = targetFromChannel(channel, client)
	assert.ErrorIs(t, err, ErrTargetUnavailable)
}
