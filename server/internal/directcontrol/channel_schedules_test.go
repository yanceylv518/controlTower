package directcontrol

import (
	"testing"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/channelschedule"
	"controltower/server/internal/tuning"
)

func TestScheduleCloseRequiresEveryModelAndGroup(t *testing.T) {
	target := channelcontrol.Channel{ID: 1, Models: "m,n", Group: "default,vip", Status: 1, Weight: 10}
	peer := channelcontrol.Channel{ID: 2, Models: "m,n", Group: "default,vip", Status: 1, Weight: 10}
	for _, tc := range []struct {
		name             string
		peers            []channelcontrol.Channel
		states           []tuning.ContinuousState
		controls         map[int64]channelschedule.Control
		blocked, waiting bool
	}{
		{name: "all covered", peers: []channelcontrol.Channel{peer}},
		{name: "last channel", blocked: true},
		{name: "closed peer", peers: []channelcontrol.Channel{{ID: 2, Models: "m,n", Group: "default,vip", Status: 2, Weight: 10}}, blocked: true},
		{name: "zero weight", peers: []channelcontrol.Channel{{ID: 2, Models: "m,n", Group: "default,vip", Status: 1, Weight: 0}}, blocked: true},
		{name: "missing model", peers: []channelcontrol.Channel{{ID: 2, Models: "m", Group: "default,vip", Status: 1, Weight: 10}}, blocked: true},
		{name: "missing group", peers: []channelcontrol.Channel{{ID: 2, Models: "m,n", Group: "default", Status: 1, Weight: 10}}, blocked: true},
		{name: "substring is not coverage", peers: []channelcontrol.Channel{{ID: 2, Models: "model-m,n", Group: "default,vip-extra", Status: 1, Weight: 10}}, blocked: true},
		{name: "any model fault blocks peer", peers: []channelcontrol.Channel{peer}, states: []tuning.ContinuousState{{ChannelID: 2, ModelName: "m", Phase: "circuit"}, {ChannelID: 2, ModelName: "n", Phase: "normal"}}, blocked: true},
		{name: "pending write", peers: []channelcontrol.Channel{peer}, states: []tuning.ContinuousState{{ChannelID: 2, Capacity: tuning.CapacityControl{PendingCommandID: "queued"}}}, blocked: true},
		{name: "peer window outside", peers: []channelcontrol.Channel{peer}, controls: map[int64]channelschedule.Control{2: {Enabled: true, Allowed: false}}, blocked: true},
		{name: "opening in another batch", peers: []channelcontrol.Channel{{ID: 2, Models: "m,n", Group: "default,vip", Status: 2, Weight: 10}}, controls: map[int64]channelschedule.Control{2: {Enabled: true, Allowed: true, Opening: true}}, blocked: true, waiting: true},
		{name: "partial pending coverage cannot hide a hard gap", peers: []channelcontrol.Channel{{ID: 2, Models: "m", Group: "default,vip", Status: 2, Weight: 10}}, controls: map[int64]channelschedule.Control{2: {Enabled: true, Allowed: true, Opening: true}}, blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			channels := append([]channelcontrol.Channel{target}, tc.peers...)
			reason, waiting := scheduleCloseBlocked(1, channels, tc.states, tc.controls)
			if (reason != "") != tc.blocked || waiting != tc.waiting {
				t.Fatalf("reason=%q waiting=%v", reason, waiting)
			}
		})
	}
}
