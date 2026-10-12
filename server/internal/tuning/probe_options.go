package tuning

import (
	"controltower/internal/channelcontrol"
	"encoding/json"
)

// Recovery probes carry the decision's immutable settings through both direct
// execution and the Agent command payload. Old commands retain former defaults.
func RecoveryProbeOptions(v Recommendation) channelcontrol.ProbeOptions {
	p := DefaultPolicy().Continuous
	if raw, err := json.Marshal(v.Evidence["evaluation"]); err == nil {
		var evaluation *EvaluationContext
		if json.Unmarshal(raw, &evaluation) == nil && evaluation != nil {
			p = evaluation.Params
		}
	}
	r := p.Rules()
	return channelcontrol.ProbeOptions{TimeoutSeconds: r.ProbeTimeoutSeconds, SlowLimit: r.ProbeSlowLimit, RoundSeconds: r.RecoveryRoundSeconds}
}
