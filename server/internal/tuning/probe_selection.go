package tuning

import "slices"

// Selection only governs routine probes and their weighting evidence. Circuit
// recovery keeps its own eligibility rules, even when no channels are selected.
func (p ContinuousDispatchParams) RoutineProbeEnabledFor(channel int64) bool {
	return p.Rules().RoutineProbeEnabled && (!p.RoutineProbeSelectedOnly || slices.Contains(p.RoutineProbeChannelIDs, channel))
}

func validateProbeSelection(p ContinuousDispatchParams, errors map[string]string) {
	if len(p.RoutineProbeChannelIDs) > 10000 {
		errors["continuous.routine_probe_channel_ids"] = "too_many_channels"
		return
	}
	seen := make(map[int64]bool, len(p.RoutineProbeChannelIDs))
	for _, id := range p.RoutineProbeChannelIDs {
		if id <= 0 || id > 9007199254740991 || seen[id] {
			errors["continuous.routine_probe_channel_ids"] = "must_be_unique_positive_safe_integers"
			return
		}
		seen[id] = true
	}
}
