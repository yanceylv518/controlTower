package directcontrol

import (
	"context"
	"sort"
	"strings"

	"controltower/server/internal/tuning"
)

// ListGroups 返回站点可用于渠道分组的完整候选集合。直连站点优先读取
// New API 的用户组接口；没有直连能力的站点退回当前渠道快照，保持 Agent
// 管理站点也能编辑已采集到的分组。
func (s Store) ListGroups(ctx context.Context, siteID string) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	controller, direct, err := s.controllerForSite(siteID)
	if err != nil {
		return nil, err
	}
	if direct {
		if lister, ok := controller.(interface {
			ListGroups(context.Context) ([]string, error)
		}); ok {
			return lister.ListGroups(ctx)
		}
	}
	channels, err := s.Store.LatestChannels(siteID)
	if err != nil {
		return nil, err
	}
	return channelGroups(channels), nil
}

// channelGroups 为没有 New API 直连的站点从全渠道快照去重分组名称。
func channelGroups(channels []tuning.Channel) []string {
	seen := make(map[string]struct{})
	groups := make([]string, 0)
	for _, channel := range channels {
		for _, raw := range strings.Split(channel.GroupName, ",") {
			group := strings.TrimSpace(raw)
			if group == "" {
				continue
			}
			if _, exists := seen[group]; exists {
				continue
			}
			seen[group] = struct{}{}
			groups = append(groups, group)
		}
	}
	sort.Strings(groups)
	return groups
}
