package providerfile

import (
	"errors"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	openai "github.com/QuantumNous/new-api/relay/channel/openai"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/contextconsensus"
	"github.com/QuantumNous/new-api/setting/model_setting"
)

var ErrTargetUnavailable = errors.New("managed provider file target is unavailable")

type Target struct {
	ChannelID    int
	ChannelType  int
	Endpoint     string
	Organization string
	Project      string
	credential   string
	client       *openai.ProviderFileClient
}

func (target Target) String() string {
	return "Target{Credential:***masked***}"
}

func (target Target) GoString() string {
	return target.String()
}

func LoadTarget(settings *model_setting.SmartRoutingSettings, runtime *contextconsensus.ManagedConsensusRuntime, httpClient *http.Client) (*Target, error) {
	if err := model_setting.ValidateProviderFileLifecycleSettings(settings); err != nil || runtime == nil || runtime.KeyDeriver == nil {
		return nil, ErrTargetUnavailable
	}
	channel, err := model.GetChannelById(settings.ProviderFileOpenAIChannelID, true)
	if err != nil {
		return nil, ErrTargetUnavailable
	}
	return targetFromChannel(channel, httpClient, true)
}

func loadDeletionTarget(channelID int, httpClient *http.Client) (*Target, error) {
	if channelID <= 0 {
		return nil, ErrTargetUnavailable
	}
	channel, err := model.GetChannelById(channelID, true)
	if err != nil {
		return nil, ErrTargetUnavailable
	}
	return targetFromChannel(channel, httpClient, false)
}

func targetFromChannel(channel *model.Channel, httpClient *http.Client, requireEnabled bool) (*Target, error) {
	if channel == nil || channel.Id <= 0 || (requireEnabled && channel.Status != common.ChannelStatusEnabled) || channel.Type != constant.ChannelTypeOpenAI ||
		channel.ChannelInfo.IsMultiKey || strings.TrimSpace(channel.Key) == "" || strings.TrimSpace(channel.Key) != channel.Key {
		return nil, ErrTargetUnavailable
	}
	endpoint := channel.GetBaseURL()
	if endpoint == "" && channel.Type >= 0 && channel.Type < len(constant.ChannelBaseURLs) {
		endpoint = constant.ChannelBaseURLs[channel.Type]
	}
	if endpoint != openai.OpenAIProviderFileOrigin && endpoint != openai.OpenAIProviderFileOrigin+"/" {
		return nil, ErrTargetUnavailable
	}
	if nonEmptyChannelSetting(channel.Setting) || nonEmptyChannelSetting(channel.ParamOverride) || nonEmptyChannelSetting(channel.HeaderOverride) ||
		(strings.TrimSpace(channel.OtherSettings) != "" && strings.TrimSpace(channel.OtherSettings) != "{}") {
		return nil, ErrTargetUnavailable
	}
	organization := ""
	if channel.OpenAIOrganization != nil {
		organization = *channel.OpenAIOrganization
	}
	project := ""
	if channel.OpenAIProject != nil {
		project = *channel.OpenAIProject
	}
	if strings.TrimSpace(project) != project {
		return nil, ErrTargetUnavailable
	}
	if httpClient == nil {
		httpClient = service.GetHttpClient()
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	client, err := openai.NewProviderFileClient(httpClient, openai.OpenAIProviderFileOrigin, channel.Key, organization, project)
	if err != nil {
		return nil, ErrTargetUnavailable
	}
	return &Target{
		ChannelID: channel.Id, ChannelType: channel.Type, Endpoint: openai.OpenAIProviderFileOrigin,
		Organization: organization, Project: project, credential: channel.Key, client: client,
	}, nil
}

func (target *Target) identity() contextconsensus.ManagedProviderFileTargetIdentity {
	if target == nil {
		return contextconsensus.ManagedProviderFileTargetIdentity{}
	}
	return contextconsensus.ManagedProviderFileTargetIdentity{
		ChannelID: target.ChannelID, ChannelType: target.ChannelType, MultiKeyIndex: 0, ChannelIsMultiKey: false,
		Endpoint: target.Endpoint, Organization: target.Organization, Project: target.Project,
	}
}

func nonEmptyChannelSetting(value *string) bool {
	return value != nil && strings.TrimSpace(*value) != ""
}
