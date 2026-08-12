package common

import "errors"

// ErrResponsesNotSupported is returned by channel adaptors that do not
// implement the OpenAI Responses API. The relay layer detects it and falls
// back to converting the Responses request to Chat Completions before sending
// it upstream, so Codex-style Responses clients can reach Chat-completions-only
// third-party models (GLM, DeepSeek, etc.).
var ErrResponsesNotSupported = errors.New("responses API not supported by this channel")
