package archivejob

import (
	"encoding/json"
	"math/big"
)

// Tool prices/counts stay in the historical pricing snapshot, so each group
// retains the evidence to compute currency fees without assuming quota units.
// Only counts are summed here; actual quota is never allocated to tools.
func addToolCallCounts(amounts map[string]string, other map[string]any) {
	raw, ok := other["tool_surcharges"]
	if !ok || raw == nil {
		amounts["tool_calls_missing"] = "1"
		return
	}
	items, ok := raw.([]any)
	total := new(big.Int)
	if ok {
		for _, item := range items {
			fields, valid := item.(map[string]any)
			if !valid {
				ok = false
				break
			}
			var text string
			switch n := fields["count"].(type) {
			case json.Number:
				text = n.String()
			case string:
				text = n
			default:
				ok = false
			}
			n, valid := new(big.Int).SetString(text, 10)
			if !valid || n.Sign() < 0 {
				ok = false
				break
			}
			total.Add(total, n)
		}
	}
	if !ok {
		amounts["tool_calls_missing"] = "1"
		amounts["tool_calls_invalid"] = "1"
		return
	}
	amounts["tool_calls"] = total.String()
}
