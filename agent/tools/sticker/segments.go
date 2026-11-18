package sticker

import (
	"fmt"
	"strconv"
)

func segFlex(data map[string]any, key string, at int) (string, error) {
	raw, ok := data[key]
	if !ok {
		return "", fmt.Errorf("第 %d 段缺少 %q 字段", at, key)
	}
	switch v := raw.(type) {
	case string:
		return v, nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("第 %d 段的 %q 应为字符串或数字", at, key)
	}
}

// segID 取消息段 data 里的必填整数（数字或数字字符串形态都收）。
func segID(data map[string]any, key string, at int) (int64, error) {
	raw, ok := data[key]
	if !ok {
		return 0, fmt.Errorf("第 %d 段缺少 %q 字段", at, key)
	}
	switch v := raw.(type) {
	case float64:
		return int64(v), nil
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("第 %d 段的 %q 应为整数, got %q", at, key, v)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("第 %d 段的 %q 应为整数", at, key)
	}
}

// anyString 宽松取字符串：字符串原样，数字转十进制，其余（含缺失）返回空串。
func anyString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return ""
}
