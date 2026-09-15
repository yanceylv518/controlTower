package logcollector

import (
	"encoding/json"
	"strconv"
	"strings"
)

// attemptCount trusts only a complete route whose final channel matches the row.
// Missing metadata is unknown, never an implicit single-attempt request.
func attemptCount(other string, channelID int64) int {
	var data struct {
		Admin struct {
			Channels []json.RawMessage `json:"use_channel"`
		} `json:"admin_info"`
	}
	if json.Unmarshal([]byte(other), &data) != nil || channelID <= 0 || len(data.Admin.Channels) == 0 {
		return 0
	}
	var last int64
	for _, raw := range data.Admin.Channels {
		value := string(raw)
		if len(value) > 0 && value[0] == '"' {
			if json.Unmarshal(raw, &value) != nil {
				return 0
			}
		}
		id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || id <= 0 {
			return 0
		}
		last = id
	}
	if last != channelID {
		return 0
	}
	return len(data.Admin.Channels)
}
