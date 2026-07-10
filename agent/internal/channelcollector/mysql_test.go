package channelcollector

import "testing"

func TestSnapshotHashIsStableAndDetectsChanges(t *testing.T) {
	items := []Snapshot{{ChannelID: 1, ChannelName: "primary", Status: "enabled", Weight: 10, ModelsText: "gpt-4o"}}
	first := snapshotHash(items)
	second := snapshotHash(append([]Snapshot(nil), items...))
	if first == "" || first != second {
		t.Fatalf("snapshot hash should be stable: %q != %q", first, second)
	}
	changed := append([]Snapshot(nil), items...)
	changed[0].Weight = 20
	if snapshotHash(changed) == first {
		t.Fatal("snapshot hash should change when channel content changes")
	}
}
