package dto

// GetMaxTokensPointer preserves the distinction between an absent output limit
// and an explicit zero. The newer max_completion_tokens field takes precedence.
func (r *GeneralOpenAIRequest) GetMaxTokensPointer() *uint {
	if r == nil {
		return nil
	}
	if r.MaxCompletionTokens != nil {
		return r.MaxCompletionTokens
	}
	return r.MaxTokens
}
