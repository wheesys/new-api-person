package contextconsensus

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"
)

const maxProviderFileReferences = 128

type ProviderFileReferenceKind string

const (
	ProviderFileReferenceProviderID  ProviderFileReferenceKind = "provider_file_id"
	ProviderFileReferenceProviderURI ProviderFileReferenceKind = "provider_file_uri"
	ProviderFileReferenceInline      ProviderFileReferenceKind = "inline_data"
	ProviderFileReferenceExternalURL ProviderFileReferenceKind = "external_url"
	ProviderFileReferenceSignedURL   ProviderFileReferenceKind = "signed_url"
)

const (
	ProviderFileReasonIDRequiresLifecycle  = "provider_file_id_requires_lifecycle"
	ProviderFileReasonURIRequiresLifecycle = "provider_file_uri_requires_lifecycle"
	ProviderFileReasonSignedURLNonPortable = "provider_file_signed_url_nonportable"
)

type ProviderFileReferenceEvidence struct {
	Protocol        types.RelayFormat         `json:"protocol"`
	Sequence        int                       `json:"sequence"`
	Kind            ProviderFileReferenceKind `json:"kind"`
	ReferenceHMAC   string                    `json:"reference_hmac,omitempty"`
	RequiredBinding BindingLevel              `json:"required_binding"`
}

type ProviderFileState struct {
	References  []ProviderFileReferenceEvidence `json:"references,omitempty"`
	ReasonCodes []string                        `json:"reason_codes,omitempty"`
	overflow    bool                            `json:"-"`
	invalid     bool                            `json:"-"`
}

type ProviderFileLifecycleCapabilities struct {
	AuthoritativeOwnership  bool `json:"authoritative_ownership"`
	AuthoritativeExpiration bool `json:"authoritative_expiration"`
	AuthoritativeDeletion   bool `json:"authoritative_deletion"`
}

func (capabilities ProviderFileLifecycleCapabilities) Complete() bool {
	return capabilities.AuthoritativeOwnership && capabilities.AuthoritativeExpiration && capabilities.AuthoritativeDeletion
}

type ProviderFileLifecycleMetadata struct {
	ReferenceHMAC     string                    `json:"reference_hmac"`
	Kind              ProviderFileReferenceKind `json:"kind"`
	OwnershipVerified bool                      `json:"ownership_verified"`
	ExpiresAtUnix     int64                     `json:"expires_at_unix"`
	DeletionSupported bool                      `json:"deletion_supported"`
}

type ProviderFileDeletionStatus string

const (
	ProviderFileDeletionDeleted  ProviderFileDeletionStatus = "deleted"
	ProviderFileDeletionNotFound ProviderFileDeletionStatus = "not_found"
	ProviderFileDeletionFailed   ProviderFileDeletionStatus = "failed"
)

type ProviderFileDeletionReport struct {
	ReferenceHMAC string                     `json:"reference_hmac"`
	Kind          ProviderFileReferenceKind  `json:"kind"`
	Status        ProviderFileDeletionStatus `json:"status"`
	DeletedAtUnix int64                      `json:"deleted_at_unix,omitempty"`
}

func ExtractProviderFileState(request ExtractionRequest) (ProviderFileState, error) {
	state := ProviderFileState{
		References:  make([]ProviderFileReferenceEvidence, 0),
		ReasonCodes: make([]string, 0),
	}
	var root map[string]any
	if err := common.Unmarshal(request.Body, &root); err != nil {
		return state, fmt.Errorf("decode provider file request: %w", err)
	}

	switch request.Protocol {
	case types.RelayFormatOpenAI:
		messages, _ := root["messages"].([]any)
		for messageIndex, messageValue := range messages {
			message, _ := messageValue.(map[string]any)
			inspectOpenAIProviderFileContent(&state, request.Protocol, messageIndex, message["content"])
		}
	case types.RelayFormatOpenAIResponses:
		input, _ := root["input"].([]any)
		for itemIndex, itemValue := range input {
			item, _ := itemValue.(map[string]any)
			inspectResponsesProviderFilePart(&state, request.Protocol, itemIndex, item)
			inspectOpenAIProviderFileContent(&state, request.Protocol, itemIndex, item["content"])
		}
	case types.RelayFormatClaude:
		messages, _ := root["messages"].([]any)
		for messageIndex, messageValue := range messages {
			message, _ := messageValue.(map[string]any)
			parts, _ := message["content"].([]any)
			for _, partValue := range parts {
				part, _ := partValue.(map[string]any)
				inspectClaudeProviderFilePart(&state, request.Protocol, messageIndex, part)
			}
		}
	case types.RelayFormatGemini:
		contents, _ := root["contents"].([]any)
		for contentIndex, contentValue := range contents {
			content, _ := contentValue.(map[string]any)
			parts, _ := content["parts"].([]any)
			for _, partValue := range parts {
				part, _ := partValue.(map[string]any)
				inspectGeminiProviderFilePart(&state, request.Protocol, contentIndex, part)
			}
		}
	default:
		return state, fmt.Errorf("unsupported provider file protocol %q", request.Protocol)
	}

	if err := state.Validate(); err != nil {
		return ProviderFileState{}, err
	}
	return state, nil
}

func (state ProviderFileState) Validate() error {
	if state.invalid {
		return fmt.Errorf("provider file reference structure is invalid")
	}
	if state.overflow || len(state.References) > maxProviderFileReferences {
		return fmt.Errorf("provider file reference count exceeds limit")
	}
	expectedReasonCodes := make(map[string]struct{}, 3)
	for _, reference := range state.References {
		if reference.Sequence < 0 || !validProviderFileProtocol(reference.Protocol) {
			return fmt.Errorf("provider file reference location is invalid")
		}
		switch reference.Kind {
		case ProviderFileReferenceProviderID, ProviderFileReferenceProviderURI:
			if reference.RequiredBinding != BindingLevelCredential || !validProviderFileReferenceHMAC(reference.ReferenceHMAC) {
				return fmt.Errorf("provider file reference evidence is invalid")
			}
			if reference.Kind == ProviderFileReferenceProviderID {
				expectedReasonCodes[ProviderFileReasonIDRequiresLifecycle] = struct{}{}
			} else {
				expectedReasonCodes[ProviderFileReasonURIRequiresLifecycle] = struct{}{}
			}
		case ProviderFileReferenceSignedURL:
			if reference.RequiredBinding != BindingLevelProvider || reference.ReferenceHMAC != "" {
				return fmt.Errorf("signed provider file URL evidence is invalid")
			}
			expectedReasonCodes[ProviderFileReasonSignedURLNonPortable] = struct{}{}
		case ProviderFileReferenceInline, ProviderFileReferenceExternalURL:
			if reference.RequiredBinding != BindingLevelNone || reference.ReferenceHMAC != "" {
				return fmt.Errorf("portable provider file evidence is invalid")
			}
		default:
			return fmt.Errorf("provider file reference kind is invalid")
		}
	}
	seenReasonCodes := make(map[string]struct{}, len(state.ReasonCodes))
	for _, reasonCode := range state.ReasonCodes {
		if reasonCode != ProviderFileReasonIDRequiresLifecycle && reasonCode != ProviderFileReasonURIRequiresLifecycle && reasonCode != ProviderFileReasonSignedURLNonPortable {
			return fmt.Errorf("provider file reason code is invalid")
		}
		if _, exists := seenReasonCodes[reasonCode]; exists {
			return fmt.Errorf("provider file reason code is duplicated")
		}
		seenReasonCodes[reasonCode] = struct{}{}
	}
	if len(seenReasonCodes) != len(expectedReasonCodes) {
		return fmt.Errorf("provider file reason codes do not match references")
	}
	for reasonCode := range expectedReasonCodes {
		if _, exists := seenReasonCodes[reasonCode]; !exists {
			return fmt.Errorf("provider file reason codes do not match references")
		}
	}
	return nil
}

func (state ProviderFileState) RequiresLifecycleValidation() bool {
	for _, reference := range state.References {
		if reference.Kind == ProviderFileReferenceProviderID || reference.Kind == ProviderFileReferenceProviderURI || reference.Kind == ProviderFileReferenceSignedURL {
			return true
		}
	}
	return false
}

func (state ProviderFileState) BindingLevelAtSequence(sequence int) BindingLevel {
	level := BindingLevelNone
	for _, reference := range state.References {
		if reference.Sequence == sequence && bindingRank(reference.RequiredBinding) > bindingRank(level) {
			level = reference.RequiredBinding
		}
	}
	return level
}

func (state ProviderFileState) Count(kind ProviderFileReferenceKind) int {
	count := 0
	for _, reference := range state.References {
		if reference.Kind == kind {
			count++
		}
	}
	return count
}

func (metadata ProviderFileLifecycleMetadata) Validate() error {
	if (metadata.Kind != ProviderFileReferenceProviderID && metadata.Kind != ProviderFileReferenceProviderURI) || !validProviderFileReferenceHMAC(metadata.ReferenceHMAC) {
		return fmt.Errorf("provider file lifecycle metadata identity is invalid")
	}
	if !metadata.OwnershipVerified || metadata.ExpiresAtUnix <= 0 || !metadata.DeletionSupported {
		return fmt.Errorf("provider file lifecycle metadata is incomplete")
	}
	return nil
}

func (report ProviderFileDeletionReport) Validate() error {
	if (report.Kind != ProviderFileReferenceProviderID && report.Kind != ProviderFileReferenceProviderURI) || !validProviderFileReferenceHMAC(report.ReferenceHMAC) {
		return fmt.Errorf("provider file deletion report identity is invalid")
	}
	switch report.Status {
	case ProviderFileDeletionDeleted:
		if report.DeletedAtUnix <= 0 {
			return fmt.Errorf("provider file deletion timestamp is required")
		}
	case ProviderFileDeletionNotFound:
		if report.DeletedAtUnix != 0 {
			return fmt.Errorf("provider file deletion timestamp is invalid")
		}
	case ProviderFileDeletionFailed:
		return fmt.Errorf("provider file deletion status is not terminal")
	default:
		return fmt.Errorf("provider file deletion status is invalid")
	}
	return nil
}

func applyProviderFileState(envelope *ContextEnvelope) {
	if envelope == nil {
		return
	}
	envelope.MediaState.InlineCount = envelope.ProviderFileState.Count(ProviderFileReferenceInline)
	providerOwnedCount := envelope.ProviderFileState.Count(ProviderFileReferenceProviderID) +
		envelope.ProviderFileState.Count(ProviderFileReferenceProviderURI)
	envelope.MediaState.ProviderBoundCount = providerOwnedCount + envelope.ProviderFileState.Count(ProviderFileReferenceSignedURL)
	if providerOwnedCount > 0 {
		requireBinding(&envelope.ProviderBinding, BindingLevelCredential, "provider_file_reference", "")
	}
	for _, reasonCode := range envelope.ProviderFileState.ReasonCodes {
		level := BindingLevelCredential
		if reasonCode == ProviderFileReasonSignedURLNonPortable {
			level = BindingLevelProvider
		}
		requireBinding(&envelope.ProviderBinding, level, reasonCode, "")
	}
}

func inspectOpenAIProviderFileContent(state *ProviderFileState, protocol types.RelayFormat, sequence int, content any) {
	parts, isArray := content.([]any)
	if !isArray {
		if part, isObject := content.(map[string]any); isObject {
			inspectOpenAIProviderFilePart(state, protocol, sequence, part)
		}
		return
	}
	for _, partValue := range parts {
		part, _ := partValue.(map[string]any)
		inspectOpenAIProviderFilePart(state, protocol, sequence, part)
	}
}

func inspectOpenAIProviderFilePart(state *ProviderFileState, protocol types.RelayFormat, sequence int, part map[string]any) {
	partType, _ := part["type"].(string)
	switch strings.ToLower(partType) {
	case "file", "input_file":
		file, _ := part["file"].(map[string]any)
		if file == nil {
			file = part
		}
		fileID, hasFileID, validFileID := providerFileStringField(file, "file_id")
		_, hasFileData, validFileData := providerFileStringField(file, "file_data")
		fileURL, hasFileURL, validFileURL := providerFileStringField(file, "file_url")
		if !validFileID || !validFileData || !validFileURL || providerFileSourceCount(hasFileID, hasFileData, hasFileURL) != 1 {
			state.invalid = true
			return
		}
		if hasFileID {
			state.addReference(protocol, sequence, ProviderFileReferenceProviderID, fileID)
		}
		if hasFileData {
			state.addReference(protocol, sequence, ProviderFileReferenceInline, "")
		}
		if hasFileURL {
			state.addURLReference(protocol, sequence, fileURL)
		}
	case "image_url", "input_image":
		state.addURLValue(protocol, sequence, part["image_url"])
	case "video_url", "input_video":
		state.addURLValue(protocol, sequence, part["video_url"])
	case "input_audio", "audio":
		audio, _ := part["input_audio"].(map[string]any)
		if audio == nil {
			audio = part
		}
		_, hasData, validData := providerFileStringField(audio, "data")
		if !hasData || !validData {
			state.invalid = true
			return
		}
		state.addReference(protocol, sequence, ProviderFileReferenceInline, "")
	}
}

func inspectResponsesProviderFilePart(state *ProviderFileState, protocol types.RelayFormat, sequence int, part map[string]any) {
	partType, _ := part["type"].(string)
	switch strings.ToLower(partType) {
	case "input_file":
		fileID, hasFileID, validFileID := providerFileStringField(part, "file_id")
		_, hasFileData, validFileData := providerFileStringField(part, "file_data")
		fileURL, hasFileURL, validFileURL := providerFileStringField(part, "file_url")
		if !validFileID || !validFileData || !validFileURL || providerFileSourceCount(hasFileID, hasFileData, hasFileURL) != 1 {
			state.invalid = true
			return
		}
		if hasFileID {
			state.addReference(protocol, sequence, ProviderFileReferenceProviderID, fileID)
		}
		if hasFileData {
			state.addReference(protocol, sequence, ProviderFileReferenceInline, "")
		}
		if hasFileURL {
			state.addURLReference(protocol, sequence, fileURL)
		}
	case "input_image":
		state.addURLValue(protocol, sequence, part["image_url"])
	case "input_audio":
		_, hasData, validData := providerFileStringField(part, "data")
		if !hasData || !validData {
			state.invalid = true
			return
		}
		state.addReference(protocol, sequence, ProviderFileReferenceInline, "")
	}
}

func inspectClaudeProviderFilePart(state *ProviderFileState, protocol types.RelayFormat, sequence int, part map[string]any) {
	partType, _ := part["type"].(string)
	if partType != "image" && partType != "document" {
		return
	}
	source, _ := part["source"].(map[string]any)
	if source == nil {
		state.invalid = true
		return
	}
	sourceType, _ := source["type"].(string)
	switch strings.ToLower(sourceType) {
	case "base64":
		_, hasData, validData := providerFileStringField(source, "data")
		if !hasData || !validData {
			state.invalid = true
			return
		}
		state.addReference(protocol, sequence, ProviderFileReferenceInline, "")
	case "url":
		rawURL, hasURL, validURL := providerFileStringField(source, "url")
		if !hasURL || !validURL {
			state.invalid = true
			return
		}
		state.addURLReference(protocol, sequence, rawURL)
	case "file":
		fileID, hasFileID, validFileID := providerFileStringField(source, "file_id")
		if !hasFileID || !validFileID {
			state.invalid = true
			return
		}
		state.addReference(protocol, sequence, ProviderFileReferenceProviderID, fileID)
	default:
		state.invalid = true
	}
}

func inspectGeminiProviderFilePart(state *ProviderFileState, protocol types.RelayFormat, sequence int, part map[string]any) {
	inlineDataValue, hasInlineData := part["inlineData"]
	if !hasInlineData {
		inlineDataValue, hasInlineData = part["inline_data"]
	}
	fileDataValue, hasFileData := part["fileData"]
	if !hasFileData {
		fileDataValue, hasFileData = part["file_data"]
	}
	if hasInlineData && hasFileData {
		state.invalid = true
		return
	}
	if hasInlineData {
		inlineData, validInlineData := inlineDataValue.(map[string]any)
		_, hasData, validData := providerFileStringField(inlineData, "data")
		if !validInlineData || !hasData || !validData {
			state.invalid = true
			return
		}
		state.addReference(protocol, sequence, ProviderFileReferenceInline, "")
	}
	if !hasFileData {
		return
	}
	fileData, validFileData := fileDataValue.(map[string]any)
	fileURI, hasFileURI, validFileURI := providerFileStringField(fileData, "fileUri")
	if !hasFileURI {
		fileURI, hasFileURI, validFileURI = providerFileStringField(fileData, "file_uri")
	}
	if !validFileData || !hasFileURI || !validFileURI {
		state.invalid = true
		return
	}
	if isSignedProviderFileURL(fileURI) {
		state.addReference(protocol, sequence, ProviderFileReferenceSignedURL, "")
		return
	}
	state.addReference(protocol, sequence, ProviderFileReferenceProviderURI, fileURI)
}

func (state *ProviderFileState) addURLValue(protocol types.RelayFormat, sequence int, value any) {
	switch typed := value.(type) {
	case string:
		state.addURLReference(protocol, sequence, typed)
	case map[string]any:
		rawURL, hasURL, validURL := providerFileStringField(typed, "url")
		if !hasURL || !validURL {
			state.invalid = true
			return
		}
		state.addURLReference(protocol, sequence, rawURL)
	default:
		state.invalid = true
	}
}

func (state *ProviderFileState) addURLReference(protocol types.RelayFormat, sequence int, rawURL string) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		state.invalid = true
		return
	}
	if strings.HasPrefix(strings.ToLower(rawURL), "data:") {
		_, payload, hasPayload := strings.Cut(rawURL, ",")
		if !hasPayload || payload == "" {
			state.invalid = true
			return
		}
		state.addReference(protocol, sequence, ProviderFileReferenceInline, "")
		return
	}
	parsedURL, err := url.Parse(rawURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		state.invalid = true
		return
	}
	if isSignedProviderFileURL(rawURL) {
		state.addReference(protocol, sequence, ProviderFileReferenceSignedURL, "")
		return
	}
	state.addReference(protocol, sequence, ProviderFileReferenceExternalURL, "")
}

func (state *ProviderFileState) addReference(protocol types.RelayFormat, sequence int, kind ProviderFileReferenceKind, rawReference string) {
	if len(state.References) >= maxProviderFileReferences {
		state.overflow = true
		return
	}
	evidence := ProviderFileReferenceEvidence{
		Protocol:        protocol,
		Sequence:        sequence,
		Kind:            kind,
		RequiredBinding: BindingLevelNone,
	}
	switch kind {
	case ProviderFileReferenceProviderID:
		evidence.ReferenceHMAC = common.GenerateHMAC("context_consensus/provider_file/v1\x00" + string(protocol) + "\x00id\x00" + rawReference)
		evidence.RequiredBinding = BindingLevelCredential
		state.ReasonCodes = appendUnique(state.ReasonCodes, ProviderFileReasonIDRequiresLifecycle)
	case ProviderFileReferenceProviderURI:
		evidence.ReferenceHMAC = common.GenerateHMAC("context_consensus/provider_file/v1\x00" + string(protocol) + "\x00uri\x00" + rawReference)
		evidence.RequiredBinding = BindingLevelCredential
		state.ReasonCodes = appendUnique(state.ReasonCodes, ProviderFileReasonURIRequiresLifecycle)
	case ProviderFileReferenceSignedURL:
		evidence.RequiredBinding = BindingLevelProvider
		state.ReasonCodes = appendUnique(state.ReasonCodes, ProviderFileReasonSignedURLNonPortable)
	}
	state.References = append(state.References, evidence)
}

func providerFileStringField(value map[string]any, key string) (string, bool, bool) {
	rawValue, present := value[key]
	if !present {
		return "", false, true
	}
	stringValue, valid := rawValue.(string)
	stringValue = strings.TrimSpace(stringValue)
	return stringValue, true, valid && stringValue != ""
}

func providerFileSourceCount(values ...bool) int {
	count := 0
	for _, present := range values {
		if present {
			count++
		}
	}
	return count
}

func isSignedProviderFileURL(rawURL string) bool {
	parsedURL, err := url.Parse(rawURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return false
	}
	signedQueryKeys := map[string]struct{}{
		"expires": {}, "googleaccessid": {}, "se": {}, "sig": {}, "signature": {},
		"x-amz-credential": {}, "x-amz-signature": {}, "x-goog-credential": {}, "x-goog-signature": {},
	}
	for queryKey := range parsedURL.Query() {
		if _, signed := signedQueryKeys[strings.ToLower(queryKey)]; signed {
			return true
		}
	}
	return false
}

func validProviderFileProtocol(protocol types.RelayFormat) bool {
	switch protocol {
	case types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, types.RelayFormatClaude, types.RelayFormatGemini:
		return true
	default:
		return false
	}
}

func validProviderFileReferenceHMAC(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
