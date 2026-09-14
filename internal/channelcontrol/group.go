package channelcontrol

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxGroupLength 与 CT 的 channel_current.group_name 列保持一致，避免
// New API 已成功写入后，快照回写因字段过长而失败。
const MaxGroupLength = 128

// ErrGroupNotFound 表示提交的分组不在当前站点 New API 渠道快照中。
var ErrGroupNotFound = errors.New("channel group not found")

// NormalizeGroup 将 New API 的逗号分隔分组整理为稳定值，并拒绝空项和控制字符；
// 整体空值是显式清空分组的合法表示。
// 该函数在 Server 和 New API 客户端边界共同使用，保证队列与直连语义一致。
func NormalizeGroup(value string) (string, error) {
	// 空字符串表示清空渠道分组；仅由空白组成的输入也按清空处理，便于
	// 管理员移除最后一个分组，同时仍拒绝把控制字符写入 New API。
	if strings.TrimSpace(value) == "" {
		if strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return "", fmt.Errorf("group contains a control character")
		}
		return "", nil
	}
	parts := strings.Split(value, ",")
	seen := make(map[string]struct{}, len(parts))
	groups := make([]string, 0, len(parts))
	for _, part := range parts {
		group := strings.TrimSpace(part)
		if group == "" {
			return "", fmt.Errorf("group must contain at least one non-empty name")
		}
		if strings.IndexFunc(group, unicode.IsControl) >= 0 {
			return "", fmt.Errorf("group contains a control character")
		}
		if _, exists := seen[group]; exists {
			continue
		}
		seen[group] = struct{}{}
		groups = append(groups, group)
	}
	result := strings.Join(groups, ",")
	if result == "" {
		return "", fmt.Errorf("group must contain at least one name")
	}
	if utf8.RuneCountInString(result) > MaxGroupLength {
		return "", fmt.Errorf("group exceeds %d characters", MaxGroupLength)
	}
	return result, nil
}

// ValidateKnownGroups 只允许使用渠道快照中已经出现过的分组名称。
// 空字符串代表显式清空分组，不需要匹配已知集合；调用方应先完成
// NormalizeGroup，传入的 knownValues 可以包含多个渠道的逗号组合。
func ValidateKnownGroups(value string, knownValues []string) error {
	normalized, err := NormalizeGroup(value)
	if err != nil {
		return err
	}
	if normalized == "" {
		return nil
	}
	known := make(map[string]struct{})
	for _, raw := range knownValues {
		groups, splitErr := NormalizeGroup(raw)
		if splitErr != nil || groups == "" {
			continue
		}
		for _, group := range strings.Split(groups, ",") {
			known[group] = struct{}{}
		}
	}
	for _, group := range strings.Split(normalized, ",") {
		if _, ok := known[group]; !ok {
			return fmt.Errorf("%w: %s", ErrGroupNotFound, group)
		}
	}
	return nil
}
