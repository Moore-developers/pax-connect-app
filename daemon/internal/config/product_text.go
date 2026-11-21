package config

import (
	"errors"
	"strings"
)

// ProductText retains event-time meaning without storing translations or a display locale.
type ProductText struct {
	Code   string            `json:"code"`
	Params map[string]string `json:"params,omitempty"`
}

// ValidateProductText bounds content at persistence and IPC boundaries.
func ValidateProductText(text *ProductText) error {
	if text == nil {
		return nil
	}
	invalid := errors.New("invalid_product_text")
	if len(text.Code) == 0 || len(text.Code) > 160 || len(text.Params) > 16 {
		return invalid
	}
	for _, r := range text.Code {
		valid := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r)
		if !valid {
			return invalid
		}
	}
	total := len(text.Code)
	for key, value := range text.Params {
		if len(key) == 0 || len(key) > 64 || len(value) > 2048 {
			return invalid
		}
		total += len(key) + len(value)
	}
	if total > 8192 {
		return invalid
	}
	return nil
}

func productTextSize(text *ProductText) int {
	if text == nil {
		return 0
	}
	size := len(text.Code)
	for key, value := range text.Params {
		size += len(key) + len(value)
	}
	return size
}
