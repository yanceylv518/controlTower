package dashboard

import (
	"fmt"
	"strconv"
)

// ArchiveOptionNames decorates only IDs present in the archive response. Names
// are current metadata; missing directories must not block historical queries.
func (h Handler) ArchiveOptionNames(site string, options map[string]map[string]bool) map[string]map[string]string {
	result := map[string]map[string]string{"user_id": {}, "channel_id": {}}
	if h.names == nil || h.names.source == nil {
		return result
	}
	instances, _ := h.instanceIDsForRequest("", site)
	for kind, labels := range result {
		for raw := range options[kind] {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				continue
			}
			if kind == "user_id" && h.names.customers != nil {
				if name := h.names.customers.name(site, id, h.names.now()); name != "" {
					labels[raw] = name
					continue
				}
			}
			for _, instance := range instances {
				var name, fallback string
				if kind == "user_id" {
					name, fallback = h.names.UserName(instance, id), fmt.Sprintf("用户 %d", id)
				} else {
					name, fallback = h.names.ChannelName(instance, id), fmt.Sprintf("渠道 %d", id)
				}
				if name != "" && name != fallback {
					labels[raw] = name
					break
				}
			}
		}
	}
	return result
}
