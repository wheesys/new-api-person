package controller

import (
	"fmt"
	"strings"
)

func validateOpenAIChannelHeaderValue(fieldName string, value *string, maximumLength int) error {
	if value == nil || *value == "" {
		return nil
	}
	if len(*value) > maximumLength || strings.TrimSpace(*value) != *value {
		return fmt.Errorf("OpenAI %s is invalid", fieldName)
	}
	for index := range len(*value) {
		if (*value)[index] < 0x21 || (*value)[index] > 0x7e {
			return fmt.Errorf("OpenAI %s is invalid", fieldName)
		}
	}
	return nil
}
