package dashboard

import (
	"testing"
	"time"

	"controltower/server/internal/storage"
)

type nameSourceFake struct{ calls map[string]int }

func (f *nameSourceFake) ChannelNames(instanceID string) (map[int64]string, error) {
	f.calls["channel_batch"]++
	return map[int64]string{5: "主渠道"}, nil
}
func (f *nameSourceFake) UserNames(instanceID string, since time.Time) (map[int64]string, error) {
	f.calls["user_batch"]++
	return map[int64]string{12: "张三"}, nil
}

func (f *nameSourceFake) InstanceByID(id string) (storage.Instance, bool, error) {
	f.calls["instance"]++
	if id == "inst" {
		return storage.Instance{ID: id, Name: "生产实例"}, true, nil
	}
	return storage.Instance{}, false, nil
}
func (f *nameSourceFake) QueryChannelSnapshots(q storage.ChannelSnapshotQuery) ([]storage.ChannelSnapshot, error) {
	f.calls["channel"]++
	if q.ChannelID == 5 {
		return []storage.ChannelSnapshot{{InstanceID: q.InstanceID, ChannelID: 5, ChannelName: "主渠道"}}, nil
	}
	return nil, nil
}
func (f *nameSourceFake) QueryLogEvents(q storage.LogQuery) ([]storage.LogEvent, error) {
	f.calls["user"]++
	if q.UserID == 12 {
		return []storage.LogEvent{{InstanceID: q.InstanceID, UserID: 12, Username: "张三"}}, nil
	}
	return nil, nil
}

func TestNameResolverMapsFallsBackAndCaches(t *testing.T) {
	f := &nameSourceFake{calls: map[string]int{}}
	r := newNameResolver(f, time.Minute)
	if got := r.ChannelName("inst", 5); got != "主渠道" {
		t.Fatalf("channel=%q", got)
	}
	if got := r.UserName("inst", 12); got != "张三" {
		t.Fatalf("user=%q", got)
	}
	if got := r.InstanceName("inst"); got != "生产实例" {
		t.Fatalf("instance=%q", got)
	}
	if got := r.ChannelName("inst", 9); got != "渠道 9" {
		t.Fatalf("fallback=%q", got)
	}
	_ = r.ChannelName("inst", 5)
	_ = r.UserName("inst", 12)
	_ = r.InstanceName("inst")
	if f.calls["channel_batch"] != 1 || f.calls["user_batch"] != 1 || f.calls["channel"] != 0 || f.calls["user"] != 0 || f.calls["instance"] != 1 {
		t.Fatalf("cache calls=%v", f.calls)
	}
}

func TestNameResolverBulkPreloadAvoidsPerKeyQueries(t *testing.T) {
	f := &nameSourceFake{calls: map[string]int{}}
	r := newNameResolver(f, time.Minute)
	for id := int64(1); id <= 100; id++ {
		_ = r.ChannelName("inst", id)
		_ = r.UserName("inst", id)
	}
	if f.calls["channel_batch"] != 1 || f.calls["user_batch"] != 1 || f.calls["channel"] != 0 || f.calls["user"] != 0 {
		t.Fatalf("bulk calls=%v", f.calls)
	}
}

func TestDisplayDimensionKeyUsesNames(t *testing.T) {
	h := NewHandler(nil).WithNameSource(&nameSourceFake{calls: map[string]int{}})
	if got := h.displayDimensionKey("instance_channel", "inst:channel:5"); got != "主渠道 (ID 5)" {
		t.Fatalf("channel=%q", got)
	}
	if got := h.displayDimensionKey("instance_user", "inst:user:12"); got != "张三 (ID 12)" {
		t.Fatalf("user=%q", got)
	}
	if got := h.displayDimensionKey("instance_user", "inst:user:99"); got != "用户 99" {
		t.Fatalf("fallback=%q", got)
	}
}

func TestDisplayDimensionNameKeepsIDsOutOfPresentation(t *testing.T) {
	h := NewHandler(nil).WithNameSource(&nameSourceFake{calls: map[string]int{}})
	if got := h.displayDimensionName("instance_channel", "inst:channel:5"); got != "主渠道" {
		t.Fatalf("channel=%q", got)
	}
	if got := h.displayDimensionName("instance_user", "inst:user:12"); got != "张三" {
		t.Fatalf("user=%q", got)
	}
	if got := h.displayDimensionName("instance_user", "inst:user:99"); got != "用户 99" {
		t.Fatalf("fallback=%q", got)
	}
}
