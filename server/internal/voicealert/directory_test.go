package voicealert

import (
	"context"
	"errors"
	"testing"
	"time"
)

type testSites struct {
	sites []string
	err   error
}

func (s *testSites) SiteIDs(context.Context) ([]string, error) { return s.sites, s.err }

type testNames struct {
	sites map[string]map[int64]string
	fail  map[string]bool
	reads int
}

func (s *testNames) CustomerNames(_ context.Context, site string) (map[int64]string, error) {
	s.reads++
	if s.fail[site] {
		return nil, errors.New("source unavailable")
	}
	return s.sites[site], nil
}

func TestDirectoryDiscoveryRefreshAndSiteIsolation(t *testing.T) {
	sites := &testSites{sites: []string{"b", "a"}}
	names := &testNames{sites: map[string]map[int64]string{"a": {7: "alice"}, "b": {7: "bob"}}, fail: map[string]bool{}}
	d := &Directory{Sites: sites, Source: names}
	ctx := context.Background()
	list, err := d.List(ctx)
	if err != nil || len(list.Customers) != 2 || list.Customers[0].Site != "a" || list.Customers[1].Label != "bob" {
		t.Fatal(list, err)
	}
	list.Customers[0].Label = "mutated"
	list, _ = d.List(ctx)
	if list.Customers[0].Label != "alice" || names.reads != 2 {
		t.Fatal("cache leaked or queried source each evaluation", list, names.reads)
	}
	names.sites["a"][8] = "new-customer"
	names.fail["b"] = true
	d.next = time.Time{}
	list, err = d.List(ctx)
	if err != nil || len(list.Customers) != 2 || list.Customers[1].UserID != 8 || len(list.UnavailableSites) != 1 || list.UnavailableSites[0] != "b" {
		t.Fatal("new customer missing or failed site reused", list, err)
	}
	sites.err = errors.New("database unavailable")
	d.next = time.Time{}
	if _, err = d.List(ctx); err == nil {
		t.Fatal("site failure hidden")
	}
}

type discoveredRepository struct {
	fakeRepository
	discovered   []Target
	directoryErr error
	queried      []Target
}

func (s *discoveredRepository) Targets(context.Context) ([]Target, error) {
	return s.discovered, s.directoryErr
}
func (s *discoveredRepository) Snapshot(ctx context.Context, t Target, now time.Time) ([]int64, time.Time, error) {
	s.queried = append(s.queried, t)
	return s.fakeRepository.Snapshot(ctx, t, now)
}

func TestRunnerUsesDirectoryAndDynamicAllRecipients(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Targets = []Target{{Site: "legacy", UserID: 99, Label: "manual"}}
	cfg.Recipients = []Recipient{{Phone: "13800000000"}}
	s := &discoveredRepository{fakeRepository: fakeRepository{config: cfg}, discovered: []Target{{Site: "a", UserID: 7, Label: "automatic-name"}}}
	c := &fakeCaller{ready: true}
	r := NewRunner(s, c)
	if err := r.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.discovered = append(s.discovered, Target{Site: "b", UserID: 7, Label: "new-account"})
	s.queried = nil
	if err := r.Once(context.Background()); err != nil || len(s.queried) != 2 || s.queried[1].Label != "new-account" {
		t.Fatal("all did not discover new account", s.queried, err)
	}
	s.config.Recipients[0].Targets = []string{"b/7"}
	s.queried = nil
	if err := r.Once(context.Background()); err != nil || len(s.queried) != 1 || s.queried[0].Site != "b" {
		t.Fatal("scope crossed site", s.queried, err)
	}
	s.directoryErr = errors.New("directory failed")
	s.queried = nil
	before := c.calls
	if err := r.Once(context.Background()); err == nil || len(s.queried) != 0 || c.calls != before {
		t.Fatal("directory failure must stop calls", err)
	}
}

func TestConfigNeedsRecipientsNotManualCustomers(t *testing.T) {
	c := DefaultConfig()
	c.Enabled = true
	c.TtsCode = "TTS_test"
	c.Recipients = []Recipient{{Phone: "13800000000"}}
	if err := c.Validate(); err != nil {
		t.Fatal("all requires manual customers", err)
	}
	c.Recipients[0].Targets = []string{"site-a/7", "site-b/7"}
	if err := c.Validate(); err != nil {
		t.Fatal("explicit directory scope rejected", err)
	}
	for _, key := range []string{"/7", "a/0", "a/7/8", "a/-1", "a/9007199254740992", "a/07"} {
		c.Recipients[0].Targets = []string{key}
		if c.Validate() == nil {
			t.Fatal("invalid scope accepted", key)
		}
	}
}
