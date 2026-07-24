package contextconsensus

import (
	"encoding/json"
	"fmt"
	"strings"
)

func contextSegment(sequence int, path, role string, kind SegmentKind, value any, compressible, providerBound bool) ContextSegment {
	return ContextSegment{
		Sequence:      sequence,
		Path:          path,
		Role:          role,
		Kind:          kind,
		Digest:        digestValue(value),
		Compressible:  compressible,
		ProviderBound: providerBound,
	}
}

func rawContextSegment(sequence int, path, role string, kind SegmentKind, value json.RawMessage, compressible, providerBound bool) ContextSegment {
	return ContextSegment{
		Sequence:      sequence,
		Path:          path,
		Role:          role,
		Kind:          kind,
		Digest:        digestBytes(value),
		Compressible:  compressible,
		ProviderBound: providerBound,
	}
}

func appendSegment(envelope *ContextEnvelope, segment ContextSegment, immutable, preserved bool) {
	if immutable {
		envelope.ImmutableInstructions = append(envelope.ImmutableInstructions, segment)
		return
	}
	if preserved {
		segment.Compressible = false
		envelope.PreservedSegments = append(envelope.PreservedSegments, segment)
		return
	}
	envelope.CompressibleSegments = append(envelope.CompressibleSegments, segment)
}

func validationError(issues []ValidationIssue) error {
	if len(issues) == 0 {
		return nil
	}
	return &ValidationError{Issues: issues}
}

func rawJSONString(value json.RawMessage) string {
	if !rawJSONPresent(value) {
		return ""
	}
	return strings.TrimSpace(string(value))
}

func inspectGenericMedia(value any, mediaState *MediaState) (hasMedia bool, providerBound bool) {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			itemHasMedia, itemProviderBound := inspectGenericMedia(item, mediaState)
			hasMedia = hasMedia || itemHasMedia
			providerBound = providerBound || itemProviderBound
		}
	case map[string]any:
		partType := strings.ToLower(strings.TrimSpace(fmt.Sprint(typed["type"])))
		switch partType {
		case "image_url", "input_image", "image":
			mediaState.ImageCount++
			hasMedia = true
		case "input_audio", "audio":
			mediaState.AudioCount++
			hasMedia = true
		case "input_video", "video_url", "video":
			mediaState.VideoCount++
			hasMedia = true
		case "input_file", "file", "document":
			mediaState.FileCount++
			hasMedia = true
		}
		if _, ok := typed["file_id"]; ok {
			providerBound = true
			mediaState.ProviderBoundCount++
		}
		if _, ok := typed["fileId"]; ok {
			providerBound = true
			mediaState.ProviderBoundCount++
		}
		for _, child := range typed {
			if _, nestedMap := child.(map[string]any); nestedMap {
				childHasMedia, childProviderBound := inspectGenericMedia(child, mediaState)
				hasMedia = hasMedia || childHasMedia
				providerBound = providerBound || childProviderBound
			}
			if _, nestedArray := child.([]any); nestedArray {
				childHasMedia, childProviderBound := inspectGenericMedia(child, mediaState)
				hasMedia = hasMedia || childHasMedia
				providerBound = providerBound || childProviderBound
			}
		}
	}
	return hasMedia, providerBound
}

func containsJSONSchema(value any) (present, strict bool) {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			itemPresent, itemStrict := containsJSONSchema(item)
			present = present || itemPresent
			strict = strict || itemStrict
		}
	case map[string]any:
		if schemaType, ok := typed["type"].(string); ok && (schemaType == "json_schema" || schemaType == "json_object") {
			present = true
		}
		if strictValue, ok := typed["strict"].(bool); ok && strictValue {
			strict = true
		}
		for _, child := range typed {
			childPresent, childStrict := containsJSONSchema(child)
			present = present || childPresent
			strict = strict || childStrict
		}
	}
	return present, strict
}
