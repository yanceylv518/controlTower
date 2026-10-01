package dashboard

import (
	"context"
	"controltower/server/internal/billing"
	"testing"
	"time"
)

type blockedSiteFillStore struct {
	AutomaticBillingStore
	started chan string
}

func (s *blockedSiteFillStore) ListBillingAutomaticTargets(context.Context) ([]billing.AutomaticTarget, error) {
	return []billing.AutomaticTarget{{InstanceID: "blocked-fill-a", Kind: "user_statement"}, {InstanceID: "blocked-fill-b", Kind: "user_statement"}}, nil
}
func (s *blockedSiteFillStore) MissingBillingDays(ctx context.Context, t billing.AutomaticTarget, _ time.Time) ([]time.Time, error) {
	s.started <- t.InstanceID
	if t.InstanceID == "blocked-fill-a" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, nil
}
func TestBillingAutomationSitesDoNotBlockEachOther(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &blockedSiteFillStore{started: make(chan string, 2)}
	done := make(chan struct{})
	go func() { (BillingAutomation{Store: s}).Run(ctx); close(done) }()
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case site := <-s.started:
			seen[site] = true
		case <-time.After(time.Second):
			t.Fatal("automatic generation blocked another site")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("automatic generation did not stop")
	}
}
