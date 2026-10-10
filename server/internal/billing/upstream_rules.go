package billing

import (
	"errors"
	"sort"
	"strings"
)

var ErrUpstreamRevisionConflict = errors.New("upstream_revision_conflict")
var ErrUpstreamPrefixConflict = errors.New("upstream_prefix_conflict")
var ErrUpstreamInUse = errors.New("upstream_in_use")
var ErrUpstreamTransferConflict = errors.New("upstream_transfer_conflict")

// ChannelPrefix matches complete names or underscore boundaries, never a loose substring.
func ChannelPrefix(name string, prefixes map[string]int64) (int64, string) {
	name = strings.TrimSpace(name)
	var owner int64
	best := ""
	for prefix, id := range prefixes {
		if (name == prefix || strings.HasPrefix(name, prefix+"_")) && len(prefix) > len(best) {
			owner, best = id, prefix
		}
	}
	return owner, best
}

func SuggestUpstreamPrefixes(up Upstream) []string {
	configured := map[string]int64{}
	for _, p := range up.ChannelPrefixes {
		configured[p] = up.ID
	}
	candidates := map[string]bool{}
	for _, c := range up.Channels {
		if id, _ := ChannelPrefix(c.ChannelName, configured); id != 0 {
			continue
		}
		p, _, _ := strings.Cut(strings.TrimSpace(c.ChannelName), "_")
		if p != "" && len([]rune(p)) <= 128 {
			candidates[p] = true
		}
	}
	out := []string{}
	for p := range candidates {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
