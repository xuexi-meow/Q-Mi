package model

import (
	"errors"
	"strings"
)

// IsContextLimitError 仅识别明确的长度拒绝，普通参数错误不得触发压缩。
func IsContextLimitError(err error) bool {
	var response *HTTPError
	if !errors.As(err, &response) || (response.Status != 400 && response.Status != 413 && response.Status != 422) {
		return false
	}
	body := strings.ToLower(response.Body)
	for _, marker := range []string{
		"context_length_exceeded", "maximum context length", "max context length",
		"context window exceeded", "context length exceeds", "exceeds the context window",
		"input is too long", "prompt is too long", "too many tokens",
		"上下文长度超", "超出最大上下文",
	} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}
