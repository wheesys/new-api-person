package helper

import (
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveUpstreamModelForBilling(t *testing.T) {
	cases := []struct {
		name    string
		origin  string
		mapping string
		want    string
	}{
		{"empty mapping string", "claude-sonnet-5", "", "claude-sonnet-5"},
		{"empty object mapping", "claude-sonnet-5", "{}", "claude-sonnet-5"},
		{"origin not in map", "claude-sonnet-5", `{"a":"b"}`, "claude-sonnet-5"},
		{"single step mapping", "claude-sonnet-5", `{"claude-sonnet-5":"deepseek-v4-flash"}`, "deepseek-v4-flash"},
		{"chain mapping to tail", "a", `{"a":"b","b":"c"}`, "c"},
		{"self loop falls back to origin", "a", `{"a":"a"}`, "a"},
		{"cycle stops before re-entering loop", "a", `{"a":"b","b":"a"}`, "b"},
		{"invalid json falls back to origin", "a", "{bad json", "a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveUpstreamModelForBilling(tc.origin, tc.mapping)
			assert.Equal(t, tc.want, got)
		})
	}
}

// 预扣费阶段 ModelMappedHelper 尚未执行，IsModelMapped 仍为初始 false，
// 计费必须能从渠道 model_mapping 自行解析出上游模型名，否则映射渠道会按原始（贵）模型倍率计费。
func TestBillingModelName_PreConsumeResolvesMappingFromContext(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("model_mapping", `{"claude-sonnet-5":"deepseek-v4-flash"}`)

	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-sonnet-5",
		ChannelMeta: &relaycommon.ChannelMeta{
			IsModelMapped:     false,
			UpstreamModelName: "claude-sonnet-5",
		},
	}
	got := billingModelName(c, info)
	require.Equal(t, "deepseek-v4-flash", got)
}

// 映射已应用阶段（结算）应直接复用 UpstreamModelName，不再重复解析。
func TestBillingModelName_AppliedMappingUsesUpstreamName(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("model_mapping", `{"claude-sonnet-5":"deepseek-v4-flash"}`)

	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-sonnet-5",
		ChannelMeta: &relaycommon.ChannelMeta{
			IsModelMapped:     true,
			UpstreamModelName: "deepseek-v4-flash",
		},
	}
	got := billingModelName(c, info)
	require.Equal(t, "deepseek-v4-flash", got)
}

// 无映射时回退原始模型名，行为与旧实现一致。
func TestBillingModelName_NoMappingReturnsOrigin(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	info := &relaycommon.RelayInfo{OriginModelName: "deepseek-v4-flash"}
	got := billingModelName(c, info)
	require.Equal(t, "deepseek-v4-flash", got)
}
