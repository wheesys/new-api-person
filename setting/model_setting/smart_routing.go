package model_setting

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type SmartRoutingSettings struct {
	VirtualModelPools                    map[string][]string                        `json:"virtual_model_pools"`
	ContextConsensusEnabled              bool                                       `json:"context_consensus_enabled"`
	AutoCompactionEnabled                bool                                       `json:"auto_compaction_enabled"`
	AllowToolResultCompaction            bool                                       `json:"allow_tool_result_compaction"`
	ManagedContextEnabled                bool                                       `json:"managed_context_enabled"`
	CompactionModelPool                  []string                                   `json:"compaction_model_pool"`
	CompactionChannelIDs                 []int                                      `json:"compaction_channel_ids"`
	AuthoritativeContextLimits           map[string]AuthoritativeContextLimitConfig `json:"authoritative_context_limits"`
	ContextSafetyMarginTokens            int                                        `json:"context_safety_margin_tokens"`
	PreservedRecentTurns                 int                                        `json:"preserved_recent_turns"`
	MaxSummaryTokens                     int                                        `json:"max_summary_tokens"`
	MaxCompactionInputTokens             int                                        `json:"max_compaction_input_tokens"`
	MaxCompactionCallsPerRequest         int                                        `json:"max_compaction_calls_per_request"`
	MaxCompactionQuota                   int                                        `json:"max_compaction_quota"`
	CompactionTimeoutSeconds             int                                        `json:"compaction_timeout_seconds"`
	ContextStateTTLSeconds               int                                        `json:"context_state_ttl_seconds"`
	ProviderFileLifecycleEnabled         bool                                       `json:"provider_file_lifecycle_enabled"`
	ProviderFileOpenAIChannelID          int                                        `json:"provider_file_openai_channel_id"`
	ProviderFileExpirationSeconds        int                                        `json:"provider_file_expiration_seconds"`
	ProviderFileMetadataVerifyTTLSeconds int                                        `json:"provider_file_metadata_verify_ttl_seconds"`
	ProviderFileDeletionLeadSeconds      int                                        `json:"provider_file_deletion_lead_seconds"`
	ProviderFileDeletionBatchSize        int                                        `json:"provider_file_deletion_batch_size"`
	ProviderFileDeletionMaxAttempts      int                                        `json:"provider_file_deletion_max_attempts"`
	ProviderFileDeletionTimeoutSeconds   int                                        `json:"provider_file_deletion_timeout_seconds"`
	ProviderFileExclusiveProjectAttested bool                                       `json:"provider_file_exclusive_project_attested"`
	ProviderFileSandboxContractVerified  bool                                       `json:"provider_file_sandbox_contract_verified"`
	ProviderFileReconciliationEnabled    bool                                       `json:"provider_file_reconciliation_enabled"`
}

type AuthoritativeContextLimitConfig struct {
	MaxContextTokens int      `json:"max_context_tokens"`
	Version          string   `json:"version"`
	ChannelIDs       []int    `json:"channel_ids"`
	RelayFormats     []string `json:"relay_formats"`
}

var smartRoutingSettings = SmartRoutingSettings{
	VirtualModelPools:                  map[string][]string{},
	CompactionModelPool:                []string{},
	CompactionChannelIDs:               []int{},
	AuthoritativeContextLimits:         map[string]AuthoritativeContextLimitConfig{},
	ContextSafetyMarginTokens:          1024,
	PreservedRecentTurns:               3,
	MaxSummaryTokens:                   2048,
	MaxCompactionInputTokens:           128000,
	MaxCompactionCallsPerRequest:       1,
	CompactionTimeoutSeconds:           30,
	ContextStateTTLSeconds:             3600,
	ProviderFileDeletionBatchSize:      10,
	ProviderFileDeletionMaxAttempts:    5,
	ProviderFileDeletionTimeoutSeconds: 30,
}

var smartRoutingSettingsSnapshot atomic.Pointer[SmartRoutingSettings]

var supportedSmartRoutingVirtualModels = map[string]struct{}{
	"auto:cheap":     {},
	"auto:balanced":  {},
	"auto:quality":   {},
	"auto:fast":      {},
	"auto:reasoning": {},
}

func init() {
	smartRoutingSettings.OnConfigUpdated()
	config.GlobalConfig.Register("smart_routing", &smartRoutingSettings)
}

func GetSmartRoutingSettings() *SmartRoutingSettings {
	return cloneSmartRoutingSettings(smartRoutingSettingsSnapshot.Load())
}

func (settings *SmartRoutingSettings) OnConfigUpdated() {
	smartRoutingSettingsSnapshot.Store(cloneSmartRoutingSettings(settings))
}

func GetSmartRoutingVirtualModelPool(virtualModel string) ([]string, bool) {
	settings := smartRoutingSettingsSnapshot.Load()
	if settings == nil || len(settings.VirtualModelPools) == 0 {
		return nil, false
	}

	pool, configured := settings.VirtualModelPools[normalizeSmartRoutingVirtualModel(virtualModel)]
	if !configured || (pool != nil && len(pool) == 0) {
		return nil, false
	}

	normalizedPool := make([]string, 0, len(pool))
	for _, modelName := range pool {
		modelName = strings.TrimSpace(modelName)
		if modelName != "" {
			normalizedPool = append(normalizedPool, modelName)
		}
	}
	if len(normalizedPool) == 0 {
		return nil, true
	}
	return append([]string(nil), normalizedPool...), true

}

func ValidateSmartRoutingVirtualModelPools(value string) error {
	var pools map[string][]string
	if err := common.UnmarshalJsonStr(value, &pools); err != nil {
		return fmt.Errorf("invalid smart routing virtual model pools: %w", err)
	}
	if pools == nil {
		return fmt.Errorf("smart routing virtual model pools must be a JSON object")
	}
	for virtualModel := range pools {
		if _, ok := supportedSmartRoutingVirtualModels[virtualModel]; !ok {
			return fmt.Errorf("unsupported smart routing virtual model key %q", virtualModel)
		}
		pool := pools[virtualModel]
		if pool == nil {
			return fmt.Errorf("smart routing virtual model pool %q must be an array", virtualModel)
		}
		for _, modelName := range pool {
			if strings.TrimSpace(modelName) == "" {
				return fmt.Errorf("smart routing virtual model pool %q contains an empty model name", virtualModel)
			}
		}
	}
	return nil
}

func ValidateSmartRoutingCompactionModelPool(value string) error {
	var pool []string
	if err := common.UnmarshalJsonStr(value, &pool); err != nil {
		return fmt.Errorf("invalid smart routing compaction model pool: %w", err)
	}
	if pool == nil {
		return fmt.Errorf("smart routing compaction model pool must be an array")
	}
	for _, modelName := range pool {
		normalizedModel := strings.ToLower(strings.TrimSpace(modelName))
		if normalizedModel == "" {
			return fmt.Errorf("smart routing compaction model pool contains an empty model name")
		}
		if normalizedModel == "auto" || normalizedModel == "smart" || strings.HasPrefix(normalizedModel, "auto:") || strings.HasPrefix(normalizedModel, "smart:") {
			return fmt.Errorf("smart routing compaction model pool must contain explicit real models")
		}
	}
	return nil
}

func ValidateSmartRoutingCompactionChannelIDs(value string) error {
	var channelIDs []int
	if err := common.UnmarshalJsonStr(value, &channelIDs); err != nil {
		return fmt.Errorf("invalid smart routing compaction channel IDs: %w", err)
	}
	if channelIDs == nil {
		return fmt.Errorf("smart routing compaction channel IDs must be an array")
	}
	seen := make(map[int]struct{}, len(channelIDs))
	for _, channelID := range channelIDs {
		if channelID <= 0 {
			return fmt.Errorf("smart routing compaction channel ID must be positive")
		}
		if _, duplicate := seen[channelID]; duplicate {
			return fmt.Errorf("smart routing compaction channel ID %d is duplicated", channelID)
		}
		seen[channelID] = struct{}{}
	}
	return nil
}

func ValidateSmartRoutingAuthoritativeContextLimits(value string) error {
	var limits map[string]AuthoritativeContextLimitConfig
	if err := common.UnmarshalJsonStr(value, &limits); err != nil {
		return fmt.Errorf("invalid smart routing authoritative context limits: %w", err)
	}
	if limits == nil {
		return fmt.Errorf("smart routing authoritative context limits must be a JSON object")
	}
	supportedRelayFormats := map[string]struct{}{
		"openai":           {},
		"openai_responses": {},
		"claude":           {},
		"gemini":           {},
	}
	for modelName, limit := range limits {
		if strings.TrimSpace(modelName) == "" {
			return fmt.Errorf("smart routing authoritative context limits contain an empty model name")
		}
		if limit.MaxContextTokens <= 0 {
			return fmt.Errorf("smart routing authoritative context limit for %q must be positive", modelName)
		}
		if strings.TrimSpace(limit.Version) == "" {
			return fmt.Errorf("smart routing authoritative context limit for %q requires a version", modelName)
		}
		if len(limit.ChannelIDs) == 0 {
			return fmt.Errorf("smart routing authoritative context limit for %q requires channel IDs", modelName)
		}
		seenChannelIDs := make(map[int]struct{}, len(limit.ChannelIDs))
		for _, channelID := range limit.ChannelIDs {
			if channelID <= 0 {
				return fmt.Errorf("smart routing authoritative context limit for %q contains a non-positive channel ID", modelName)
			}
			if _, duplicate := seenChannelIDs[channelID]; duplicate {
				return fmt.Errorf("smart routing authoritative context limit for %q contains duplicate channel ID %d", modelName, channelID)
			}
			seenChannelIDs[channelID] = struct{}{}
		}
		if len(limit.RelayFormats) == 0 {
			return fmt.Errorf("smart routing authoritative context limit for %q requires relay formats", modelName)
		}
		seenRelayFormats := make(map[string]struct{}, len(limit.RelayFormats))
		for _, relayFormat := range limit.RelayFormats {
			relayFormat = strings.TrimSpace(relayFormat)
			if _, supported := supportedRelayFormats[relayFormat]; !supported {
				return fmt.Errorf("smart routing authoritative context limit for %q contains unsupported relay format %q", modelName, relayFormat)
			}
			if _, duplicate := seenRelayFormats[relayFormat]; duplicate {
				return fmt.Errorf("smart routing authoritative context limit for %q contains duplicate relay format %q", modelName, relayFormat)
			}
			seenRelayFormats[relayFormat] = struct{}{}
		}
	}
	return nil
}

func ValidateProviderFileLifecycleReadiness(settings *SmartRoutingSettings) error {
	if settings == nil || !settings.ProviderFileLifecycleEnabled {
		return fmt.Errorf("provider file lifecycle is disabled")
	}
	if settings.ProviderFileOpenAIChannelID <= 0 {
		return fmt.Errorf("provider file lifecycle requires a dedicated OpenAI channel")
	}
	if settings.ProviderFileExpirationSeconds < 60 || settings.ProviderFileExpirationSeconds > 30*24*60*60 {
		return fmt.Errorf("provider file lifecycle expiration must be between 60 seconds and 30 days")
	}
	if settings.ProviderFileMetadataVerifyTTLSeconds < 0 || settings.ProviderFileMetadataVerifyTTLSeconds >= settings.ProviderFileExpirationSeconds {
		return fmt.Errorf("provider file lifecycle metadata verification TTL is invalid")
	}
	if settings.ProviderFileDeletionLeadSeconds < 0 || settings.ProviderFileDeletionLeadSeconds >= settings.ProviderFileExpirationSeconds {
		return fmt.Errorf("provider file lifecycle deletion lead is invalid")
	}
	if settings.ProviderFileDeletionBatchSize <= 0 || settings.ProviderFileDeletionBatchSize > 100 {
		return fmt.Errorf("provider file lifecycle deletion batch size is invalid")
	}
	if settings.ProviderFileDeletionMaxAttempts <= 0 || settings.ProviderFileDeletionMaxAttempts > 100 {
		return fmt.Errorf("provider file lifecycle deletion attempts are invalid")
	}
	if settings.ProviderFileDeletionTimeoutSeconds <= 0 || settings.ProviderFileDeletionTimeoutSeconds > 120 {
		return fmt.Errorf("provider file lifecycle deletion timeout is invalid")
	}
	if !settings.ProviderFileExclusiveProjectAttested {
		return fmt.Errorf("provider file lifecycle requires an exclusive OpenAI project attestation")
	}
	if !settings.ProviderFileSandboxContractVerified {
		return fmt.Errorf("provider file lifecycle requires verified sandbox deletion contracts")
	}
	return nil
}

func cloneSmartRoutingSettings(settings *SmartRoutingSettings) *SmartRoutingSettings {
	cloned := &SmartRoutingSettings{
		VirtualModelPools:                  map[string][]string{},
		CompactionModelPool:                []string{},
		CompactionChannelIDs:               []int{},
		AuthoritativeContextLimits:         map[string]AuthoritativeContextLimitConfig{},
		ContextSafetyMarginTokens:          1024,
		PreservedRecentTurns:               3,
		MaxSummaryTokens:                   2048,
		MaxCompactionInputTokens:           128000,
		MaxCompactionCallsPerRequest:       1,
		CompactionTimeoutSeconds:           30,
		ContextStateTTLSeconds:             3600,
		ProviderFileDeletionBatchSize:      10,
		ProviderFileDeletionMaxAttempts:    5,
		ProviderFileDeletionTimeoutSeconds: 30,
	}
	if settings == nil {
		return cloned
	}
	*cloned = *settings
	cloned.VirtualModelPools = make(map[string][]string, len(settings.VirtualModelPools))
	for virtualModel, pool := range settings.VirtualModelPools {
		cloned.VirtualModelPools[virtualModel] = append([]string(nil), pool...)
	}
	cloned.CompactionModelPool = append([]string(nil), settings.CompactionModelPool...)
	cloned.CompactionChannelIDs = append([]int(nil), settings.CompactionChannelIDs...)
	cloned.AuthoritativeContextLimits = make(map[string]AuthoritativeContextLimitConfig, len(settings.AuthoritativeContextLimits))
	for modelName, limit := range settings.AuthoritativeContextLimits {
		limit.ChannelIDs = append([]int(nil), limit.ChannelIDs...)
		limit.RelayFormats = append([]string(nil), limit.RelayFormats...)
		cloned.AuthoritativeContextLimits[modelName] = limit
	}
	return cloned
}

func normalizeSmartRoutingVirtualModel(virtualModel string) string {
	normalized := strings.ToLower(strings.TrimSpace(virtualModel))
	if normalized == "auto" || normalized == "smart" {
		return "auto:balanced"
	}
	if strings.HasPrefix(normalized, "smart:") {
		return "auto:" + strings.TrimPrefix(normalized, "smart:")
	}
	return normalized
}
