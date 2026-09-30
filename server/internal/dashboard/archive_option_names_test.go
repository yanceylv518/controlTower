package dashboard

import (
	"context"
	"errors"
	"testing"

	"controltower/server/internal/storage"
)

type archiveNameInstances struct{ InstanceStore }

func (archiveNameInstances) ListInstances() ([]storage.Instance, error) {
	return []storage.Instance{{ID: "inst", SiteID: "site-a"}, {ID: "other", SiteID: "site-b"}}, nil
}

type archiveNameSource struct{ customerInstanceSource }

func (f *archiveNameSource) ChannelNames(instance string) (map[int64]string, error) {
	f.calls["channel_batch"]++
	return map[int64]string{5: instance + "-渠道"}, nil
}

func TestArchiveOptionNamesSiteIsolationAndFallback(t *testing.T) {
	source := &archiveNameSource{customerInstanceSource{nameSourceFake{calls: map[string]int{}}}}
	profile := customerNameFunc(func(_ context.Context, site string) (map[int64]string, error) {
		return map[int64]string{12: site + "-用户", 99: "不在归档候选中"}, nil
	})
	h := NewHandler(nil).WithNameSource(source).WithCustomerNameSource(profile).WithInstanceStore(archiveNameInstances{})
	options := map[string]map[string]bool{"user_id": {"12": true, "404": true}, "channel_id": {"5": true, "0": true, "9007199254740993": true}, "model_name": {"m": true}}
	for _, site := range []string{"site-a", "site-b"} {
		got := h.ArchiveOptionNames(site, options)
		instance := "inst"
		if site == "site-b" {
			instance = "other"
		}
		if got["user_id"]["12"] != site+"-用户" || got["channel_id"]["5"] != instance+"-渠道" {
			t.Fatalf("wrong site names: %v", got)
		}
		if len(got["user_id"]) != 1 || len(got["channel_id"]) != 1 || len(got) != 2 {
			t.Fatalf("unknown or unrequested names: %v", got)
		}
	}
	// A source outage still permits archived IDs and cached CT names.
	h = NewHandler(nil).WithNameSource(source).WithCustomerNameSource(customerNameFunc(func(context.Context, string) (map[int64]string, error) { return nil, errors.New("offline") })).WithInstanceStore(archiveNameInstances{})
	if got := h.ArchiveOptionNames("site-a", options); got["user_id"]["12"] != "张三" || got["channel_id"]["5"] != "inst-渠道" {
		t.Fatal(got)
	}
}
