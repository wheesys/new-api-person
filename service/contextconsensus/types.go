package contextconsensus

import (
	"context"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/types"
)

type SegmentKind string

const (
	SegmentKindInstruction   SegmentKind = "instruction"
	SegmentKindMessage       SegmentKind = "message"
	SegmentKindToolCall      SegmentKind = "tool_call"
	SegmentKindToolResult    SegmentKind = "tool_result"
	SegmentKindSchema        SegmentKind = "schema"
	SegmentKindMedia         SegmentKind = "media"
	SegmentKindProviderState SegmentKind = "provider_state"
)

type ContextSegment struct {
	Sequence      int         `json:"sequence"`
	Path          string      `json:"path"`
	Role          string      `json:"role,omitempty"`
	Kind          SegmentKind `json:"kind"`
	Digest        string      `json:"digest"`
	Compressible  bool        `json:"compressible"`
	ProviderBound bool        `json:"provider_bound"`
}

type ToolExchangeStatus string

const (
	ToolExchangePending   ToolExchangeStatus = "pending"
	ToolExchangeCompleted ToolExchangeStatus = "completed"
	ToolExchangeFailed    ToolExchangeStatus = "failed"
)

type ToolExchange struct {
	Protocol           types.RelayFormat  `json:"protocol"`
	Sequence           int                `json:"sequence"`
	ParallelGroup      int                `json:"parallel_group"`
	CallID             string             `json:"-"`
	FunctionName       string             `json:"-"`
	ArgumentsDigest    string             `json:"arguments_digest,omitempty"`
	ResultDigest       string             `json:"result_digest,omitempty"`
	Status             ToolExchangeStatus `json:"status"`
	RawCallPresent     bool               `json:"raw_call_present"`
	RawResultPresent   bool               `json:"raw_result_present"`
	OpaqueStatePresent bool               `json:"opaque_state_present"`
	RequiredBinding    BindingLevel       `json:"required_binding"`
}

type ToolGraph struct {
	Exchanges              []ToolExchange `json:"exchanges,omitempty"`
	SchemaDigest           string         `json:"schema_digest,omitempty"`
	AmbiguousFunctionNames []string       `json:"ambiguous_function_names,omitempty"`
}

type SchemaState struct {
	Present bool   `json:"present"`
	Strict  bool   `json:"strict"`
	Digest  string `json:"digest,omitempty"`
}

type MediaState struct {
	ImageCount         int `json:"image_count"`
	AudioCount         int `json:"audio_count"`
	VideoCount         int `json:"video_count"`
	FileCount          int `json:"file_count"`
	InlineCount        int `json:"inline_count"`
	ProviderBoundCount int `json:"provider_bound_count"`
}

func (state MediaState) TotalCount() int {
	return state.ImageCount + state.AudioCount + state.VideoCount + state.FileCount
}

type BindingLevel string

const (
	BindingLevelNone        BindingLevel = "none"
	BindingLevelModelFamily BindingLevel = "model_family"
	BindingLevelProvider    BindingLevel = "provider"
	BindingLevelChannel     BindingLevel = "channel"
	BindingLevelCredential  BindingLevel = "credential"
)

type ProtocolBinding struct {
	BindingLevel         BindingLevel      `json:"binding_level"`
	RelayFormat          types.RelayFormat `json:"relay_format"`
	StateReferenceHashes []string          `json:"state_reference_hashes,omitempty"`
	ReasonCodes          []string          `json:"reason_codes,omitempty"`
}

func (binding ProtocolBinding) Required() bool {
	return binding.BindingLevel != "" && binding.BindingLevel != BindingLevelNone
}

type TokenBreakdown struct {
	TextTokens             int `json:"text_tokens"`
	ToolTokens             int `json:"tool_tokens"`
	SchemaTokens           int `json:"schema_tokens"`
	MediaTokens            int `json:"media_tokens"`
	ProtocolOverheadTokens int `json:"protocol_overhead_tokens"`
}

func (breakdown TokenBreakdown) PromptTokens() int {
	return breakdown.TextTokens + breakdown.ToolTokens + breakdown.SchemaTokens + breakdown.MediaTokens + breakdown.ProtocolOverheadTokens
}

type ContextEnvelope struct {
	Protocol              types.RelayFormat `json:"protocol"`
	OriginalModel         string            `json:"original_model,omitempty"`
	ImmutableInstructions []ContextSegment  `json:"immutable_instructions,omitempty"`
	CompressibleSegments  []ContextSegment  `json:"compressible_segments,omitempty"`
	PreservedSegments     []ContextSegment  `json:"preserved_segments,omitempty"`
	ToolState             ToolGraph         `json:"tool_state"`
	SchemaState           SchemaState       `json:"schema_state"`
	MediaState            MediaState        `json:"media_state"`
	ProviderBinding       ProtocolBinding   `json:"provider_binding"`
	TokenBreakdown        TokenBreakdown    `json:"token_breakdown"`
	RequestedMaxOutput    *uint             `json:"requested_max_output_tokens,omitempty"`
	SourceDigest          string            `json:"source_digest"`
}

type ExtractionRequest struct {
	Protocol      types.RelayFormat
	OriginalModel string
	Body          []byte
}

type ContextRoutingConstraint struct {
	Mode                  string            `json:"mode"`
	ValidationMode        string            `json:"validation_mode"`
	ValidationResult      string            `json:"validation_result"`
	Protocol              types.RelayFormat `json:"protocol"`
	EffectivePromptTokens int               `json:"effective_prompt_tokens"`
	RequiredBinding       BindingLevel      `json:"required_binding"`
	SwitchAllowed         bool              `json:"switch_allowed"`
	WouldBlock            bool              `json:"would_block"`
	CompactionRequired    bool              `json:"compaction_required"`
	PreservedSegmentCount int               `json:"preserved_segment_count"`
	ToolExchangeCount     int               `json:"tool_exchange_count"`
	ReasonCodes           []string          `json:"reason_codes,omitempty"`
}

func (envelope *ContextEnvelope) RoutingConstraint(effectivePromptTokens int) ContextRoutingConstraint {
	bindingLevel := envelope.ProviderBinding.BindingLevel
	if bindingLevel == "" {
		bindingLevel = BindingLevelNone
	}
	bindingRequired := envelope.ProviderBinding.Required()
	validationResult := "valid"
	if bindingRequired {
		validationResult = "would_block"
	}
	return ContextRoutingConstraint{
		Mode:                  "stateless_full_context",
		ValidationMode:        "validate_only",
		ValidationResult:      validationResult,
		Protocol:              envelope.Protocol,
		EffectivePromptTokens: effectivePromptTokens,
		RequiredBinding:       bindingLevel,
		SwitchAllowed:         !bindingRequired,
		WouldBlock:            bindingRequired,
		PreservedSegmentCount: len(envelope.PreservedSegments),
		ToolExchangeCount:     len(envelope.ToolState.Exchanges),
		ReasonCodes:           append([]string(nil), envelope.ProviderBinding.ReasonCodes...),
	}
}

type ValidationIssue struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

type ValidationError struct {
	Issues []ValidationIssue
}

func (validationError *ValidationError) Error() string {
	if validationError == nil || len(validationError.Issues) == 0 {
		return "context envelope validation failed"
	}
	messages := make([]string, 0, len(validationError.Issues))
	for _, issue := range validationError.Issues {
		message := issue.Code
		if issue.Path != "" {
			message += " at " + issue.Path
		}
		messages = append(messages, message)
	}
	return "context envelope validation failed: " + strings.Join(messages, ", ")
}

type TokenCountRequest struct {
	Protocol    types.RelayFormat
	Model       string
	RequestBody []byte
	Envelope    *ContextEnvelope
}

type TokenCounter interface {
	CountPromptTokens(ctx context.Context, request TokenCountRequest) (TokenBreakdown, error)
}

type TokenBudgetRequest struct {
	ContextLimitTokens     int
	RequestedMaxOutput     *uint
	DefaultMaxOutputTokens int
	SafetyMarginTokens     int
}

type TokenBudget struct {
	Breakdown          TokenBreakdown `json:"breakdown"`
	PromptTokens       int            `json:"prompt_tokens"`
	OutputTokens       int            `json:"output_tokens"`
	SafetyMarginTokens int            `json:"safety_margin_tokens"`
	RequiredTokens     int            `json:"required_tokens"`
	ContextLimitTokens int            `json:"context_limit_tokens"`
	RemainingTokens    int            `json:"remaining_tokens"`
	Fits               bool           `json:"fits"`
	ExplicitZeroOutput bool           `json:"explicit_zero_output"`
}

func validateNonNegative(name string, value int) error {
	if value < 0 {
		return fmt.Errorf("%s must not be negative", name)
	}
	return nil
}
