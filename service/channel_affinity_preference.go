package service

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

type ChannelAffinityKind string

const (
	ChannelAffinitySession ChannelAffinityKind = "session"
	ChannelAffinityCache   ChannelAffinityKind = "cache"
)

type ChannelAffinityPreference struct {
	ChannelID int
	Found     bool
	Kind      ChannelAffinityKind
}

func ResolveChannelAffinityPreference(c *gin.Context, modelName string, usingGroup string) ChannelAffinityPreference {
	return resolveChannelAffinityPreference(c, modelName, usingGroup, true)
}

func PeekChannelAffinityPreference(c *gin.Context, modelName string, usingGroup string) ChannelAffinityPreference {
	return resolveChannelAffinityPreference(c, modelName, usingGroup, false)
}

func resolveChannelAffinityPreference(c *gin.Context, modelName string, usingGroup string, bindContext bool) ChannelAffinityPreference {
	setting := operation_setting.GetChannelAffinitySetting()
	if setting == nil || !setting.Enabled {
		return ChannelAffinityPreference{}
	}
	path := ""
	if c != nil && c.Request != nil && c.Request.URL != nil {
		path = c.Request.URL.Path
	}
	userAgent := ""
	if c != nil && c.Request != nil {
		userAgent = c.Request.UserAgent()
	}

	for _, rule := range setting.Rules {
		if !matchAnyRegexCached(rule.ModelRegex, modelName) {
			continue
		}
		if len(rule.PathRegex) > 0 && !matchAnyRegexCached(rule.PathRegex, path) {
			continue
		}
		if len(rule.UserAgentInclude) > 0 && !matchAnyIncludeFold(rule.UserAgentInclude, userAgent) {
			continue
		}
		var affinityValue string
		var usedSource operation_setting.ChannelAffinityKeySource
		for _, source := range rule.KeySources {
			affinityValue = extractChannelAffinityValue(c, source)
			if affinityValue != "" {
				usedSource = source
				break
			}
		}
		if affinityValue == "" {
			continue
		}
		if rule.ValueRegex != "" && !matchAnyRegexCached([]string{rule.ValueRegex}, affinityValue) {
			continue
		}

		ttlSeconds := rule.TTLSeconds
		if ttlSeconds <= 0 {
			ttlSeconds = setting.DefaultTTLSeconds
		}
		cacheKeySuffix := buildChannelAffinityCacheKeySuffix(rule, modelName, usingGroup, affinityValue)
		cacheKeyFull := channelAffinityCacheNamespace + ":" + cacheKeySuffix
		affinityKind := ChannelAffinitySession
		if strings.Contains(strings.ToLower(usedSource.Key), "cache") || strings.Contains(strings.ToLower(usedSource.Path), "cache") {
			affinityKind = ChannelAffinityCache
		}
		if bindContext {
			setChannelAffinityContext(c, channelAffinityMeta{
				CacheKey:       cacheKeyFull,
				TTLSeconds:     ttlSeconds,
				RuleName:       rule.Name,
				SkipRetry:      rule.SkipRetryOnFailure,
				ParamTemplate:  cloneStringAnyMap(rule.ParamOverrideTemplate),
				KeySourceType:  strings.TrimSpace(usedSource.Type),
				KeySourceKey:   strings.TrimSpace(usedSource.Key),
				KeySourcePath:  strings.TrimSpace(usedSource.Path),
				KeyHint:        buildChannelAffinityKeyHint(affinityValue),
				KeyFingerprint: affinityFingerprint(affinityValue),
				UsingGroup:     usingGroup,
				ModelName:      modelName,
				RequestPath:    path,
			})
		}

		channelID, found, err := getChannelAffinityCache().Get(cacheKeySuffix)
		if err != nil {
			common.SysError(fmt.Sprintf("channel affinity cache get failed: key=%s, err=%v", cacheKeyFull, err))
			return ChannelAffinityPreference{Kind: affinityKind}
		}
		if found {
			return ChannelAffinityPreference{ChannelID: channelID, Found: true, Kind: affinityKind}
		}
		return ChannelAffinityPreference{Kind: affinityKind}
	}
	return ChannelAffinityPreference{}
}

func GetPreferredChannelByAffinity(c *gin.Context, modelName string, usingGroup string) (int, bool) {
	preference := ResolveChannelAffinityPreference(c, modelName, usingGroup)
	return preference.ChannelID, preference.Found
}
