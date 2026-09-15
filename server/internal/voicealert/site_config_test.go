package voicealert

import (
	"context"
	"testing"
)

func TestLegacyDraftDoesNotActivateOrBroaden(t *testing.T) {
	c := DefaultConfig()
	c.Enabled = true
	c.Percent = 50
	c.Recipients = []Recipient{{Phone: "13800000000"}, {Phone: "13900000000", Targets: []string{"a/1", "b/2"}}, {Phone: "13700000000", Targets: []string{"b/3"}}}
	got := legacyDraft(c, "a")
	if got.Enabled || got.Percent != 50 || len(got.Recipients) != 2 || len(got.Recipients[1].Targets) != 1 || got.Recipients[1].Targets[0] != "a/1" {
		t.Fatal(got)
	}
	if len(c.Recipients[1].Targets) != 2 {
		t.Fatal("mutated legacy")
	}
	if err := got.ValidateSite("a"); err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateSite("a"); err == nil {
		t.Fatal("cross-site accepted")
	}
}

type siteRepository struct {
	discoveredRepository
	configs map[string]Config
}

func (s *siteRepository) Configs(context.Context) (map[string]Config, error) { return s.configs, nil }
func TestRunnerOnlyUsesEnabledSiteConfig(t *testing.T) {
	a := DefaultConfig()
	a.Enabled = true
	a.Recipients = []Recipient{{Phone: "13800000000"}}
	b := a
	b.Enabled = false
	b.Recipients = []Recipient{{Phone: "13900000000"}}
	s := &siteRepository{configs: map[string]Config{"a": a, "b": b}}
	s.discovered = []Target{{Site: "a", UserID: 1}, {Site: "b", UserID: 1}, {Site: "new", UserID: 1}}
	caller := &fakeCaller{ready: true}
	r := NewRunner(s, caller)
	if err := r.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.queried) != 1 || s.queried[0].Site != "a" || len(caller.phones) != 1 || caller.phones[0] != "13800000000" {
		t.Fatal(s.queried, caller.phones)
	}
	a.Enabled = false
	b.Enabled = true
	s.configs = map[string]Config{"a": a, "b": b}
	s.queried = nil
	s.claimed = false
	if err := r.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.queried) != 1 || s.queried[0].Site != "b" || caller.phones[1] != "13900000000" {
		t.Fatal(s.queried, caller.phones)
	}
}
