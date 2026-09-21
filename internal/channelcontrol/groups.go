package channelcontrol

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// ListGroups 读取 New API 的权威用户组列表，供渠道分组编辑器展示和服务端校验。
// 分组接口是只读请求，不会创建或修改 New API 中的任何用户组。
func (c *Client) ListGroups(ctx context.Context) ([]string, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	if c.adminUserID <= 0 {
		return nil, fmt.Errorf("new-api admin user id is not configured")
	}
	var response struct {
		Success bool     `json:"success"`
		Message string   `json:"message"`
		Data    []string `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, c.baseURL+"/api/group/", nil, &response, true); err != nil {
		return nil, err
	}
	if !response.Success {
		return nil, fmt.Errorf("new-api group list failed: %s", response.Message)
	}
	seen := make(map[string]struct{}, len(response.Data))
	groups := make([]string, 0, len(response.Data))
	for _, value := range response.Data {
		group := strings.TrimSpace(value)
		if group == "" {
			continue
		}
		if _, exists := seen[group]; exists {
			continue
		}
		seen[group] = struct{}{}
		groups = append(groups, group)
	}
	sort.Strings(groups)
	return groups, nil
}
