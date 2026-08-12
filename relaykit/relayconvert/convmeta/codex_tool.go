package convmeta

import (
	"crypto/sha256"
	"encoding/hex"
)

// ChatToolNameMaxLen is the hard OpenAI function-name length limit. Longer
// namespaced names are truncated and suffixed with a short hash to stay unique.
const ChatToolNameMaxLen = 64

// ChatToolNamespaceSeparator joins a namespace and name into a flat chat name.
const ChatToolNamespaceSeparator = "__"

// ChatToolCustomInputField names the single string parameter used when a
// Responses custom tool is wrapped as a function tool.
const ChatToolCustomInputField = "input"

// CodexToolKind classifies a Responses-style tool definition.
type CodexToolKind int

const (
	CodexToolFunction CodexToolKind = iota
	CodexToolCustom
	CodexToolToolSearch
)

// CodexToolSpec records the original Responses identity of a tool after its
// chat-facing name has been flattened.
type CodexToolSpec struct {
	Kind      CodexToolKind
	Name      string
	Namespace string
}

// CodexToolContext records the bidirectional mapping between Responses-style
// tools (function/custom/tool_search with optional namespaces) and the
// flattened Chat Completions tool names they are sent as. It mirrors the role
// of cc-switch's CodexToolContext: namespaces are flattened for the upstream,
// and the flat name is resolved back to the original namespace/kind when the
// model's tool call or a later turn must be converted back to Responses format.
type CodexToolContext struct {
	byChatName map[string]CodexToolSpec
}

// NewCodexToolContext returns an empty mapping context.
func NewCodexToolContext() *CodexToolContext {
	return &CodexToolContext{byChatName: make(map[string]CodexToolSpec)}
}

// Record registers the chat-facing name for a Responses tool.
func (c *CodexToolContext) Record(chatName string, spec CodexToolSpec) {
	if c == nil {
		return
	}
	if c.byChatName == nil {
		c.byChatName = make(map[string]CodexToolSpec)
	}
	c.byChatName[chatName] = spec
}

// Lookup resolves a flat chat tool name back to its Responses tool identity.
func (c *CodexToolContext) Lookup(chatName string) (CodexToolSpec, bool) {
	if c == nil {
		return CodexToolSpec{}, false
	}
	spec, ok := c.byChatName[chatName]
	return spec, ok
}

// FlattenNamespaceName joins a namespace and name with the separator,
// truncating to ChatToolNameMaxLen with a short hash suffix when needed.
func (c *CodexToolContext) FlattenNamespaceName(namespace, name string) string {
	joined := name
	if namespace != "" {
		joined = namespace + ChatToolNamespaceSeparator + name
	}
	if len(joined) <= ChatToolNameMaxLen {
		return joined
	}
	sum := sha256.Sum256([]byte(joined))
	hash := hex.EncodeToString(sum[:])
	return joined[:ChatToolNameMaxLen-len(hash)] + hash
}
