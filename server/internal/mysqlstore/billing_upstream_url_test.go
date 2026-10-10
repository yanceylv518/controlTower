package mysqlstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"controltower/server/internal/billing"
	"github.com/go-sql-driver/mysql"
)

type upstreamURLSource struct {
	channels []billing.ConfiguredChannel
	err      error
	calls    int
}

func (s *upstreamURLSource) CurrentChannels(_ context.Context, site string) ([]billing.ConfiguredChannel, error) {
	s.calls++
	return s.channels, s.err
}

func TestBillingUpstreamURLMySQL(t *testing.T) {
	dsn := os.Getenv("CT_UPSTREAM_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_UPSTREAM_TEST_DSN for isolated local MySQL")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	if cfg.Net != "tcp" || !(strings.HasPrefix(cfg.Addr, "127.0.0.1:") || strings.HasPrefix(cfg.Addr, "localhost:")) {
		t.Fatal("requires local MySQL")
	}
	cfg.DBName = ""
	admin, err := Open(cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("ct_upstream_url_%d", time.Now().UnixNano())
	if _, err = admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, e := admin.Exec("DROP DATABASE `" + name + "`"); e != nil {
			t.Error(e)
		}
	}()
	cfg.DBName = name
	cfg.ParseTime = true
	db, err := Open(cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	// Match the release migration set, excluding the unshipped old experiment.
	dir := t.TempDir()
	files, _ := filepath.Glob("../../migrations/*.sql")
	for _, file := range files {
		if filepath.Base(file) == "097_error_statistics.sql" {
			continue
		}
		data, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, filepath.Base(file)), data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if err = ApplyDir(ctx, db, dir); err != nil {
		t.Fatal(err)
	}
	if err = ApplyDir(ctx, db, dir); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	load := func(site string, id int64) billing.Upstream {
		t.Helper()
		items, e := s.ListBillingUpstreamsConfig(ctx, site)
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range items {
			if v.ID == id {
				return v
			}
		}
		t.Fatalf("missing upstream %d", id)
		return billing.Upstream{}
	}
	put := func(v billing.Upstream) billing.Upstream {
		t.Helper()
		saved, e := s.PutBillingUpstream(ctx, v)
		if e != nil {
			t.Fatal(e)
		}
		return saved
	}
	t.Run("safe legacy seed and review markers", func(t *testing.T) {
		_, e := db.Exec(`INSERT INTO billing_upstreams(instance_id,name,enabled,remark,created_at,updated_at,updated_by) VALUES('seed','alpha_cn',0,'',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),'test')`)
		if e != nil {
			t.Fatal(e)
		}
		var id int64
		if e = db.QueryRow(`SELECT id FROM billing_upstreams WHERE instance_id='seed'`).Scan(&id); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(`INSERT INTO billing_upstream_channel_bindings(instance_id,upstream_id,channel_id,channel_name,created_at) VALUES('seed',?,1,'alpha_cn_gpt',UTC_TIMESTAMP(6)),('seed',?,2,'other_special',UTC_TIMESTAMP(6))`, id, id); e != nil {
			t.Fatal(e)
		}
		migration, e := os.ReadFile("../../migrations/130_billing_upstream_prefixes.sql")
		if e != nil {
			t.Fatal(e)
		}
		if e = ApplySQL(ctx, db, string(migration)); e != nil {
			t.Fatal(e)
		}
		v := load("seed", id)
		if len(v.ChannelPrefixes) != 1 || v.ChannelPrefixes[0] != "alpha_cn" || len(v.SuggestedPrefixes) != 1 || v.SuggestedPrefixes[0] != "other" {
			t.Fatalf("unsafe prefix seed: %+v", v)
		}
		if v.Channels[0].AssociationSource != "legacy" {
			t.Fatal("legacy provenance missing")
		}
		if _, e = db.Exec(`INSERT INTO billing_upstream_prefixes(instance_id,prefix,upstream_id) VALUES('seed','explicit-old-alias',?)`, id); e != nil {
			t.Fatal(e)
		}
		v = load("seed", id)
		if len(v.ReviewPrefixes) == 0 {
			t.Fatal("existing aliases lost review marker")
		}
		pending := []billing.ConfiguredChannel{{ChannelID: 3, ChannelName: "explicit-old-alias_future"}}
		if e = s.SyncBillingUpstreamChannels(ctx, "seed", pending); e != nil {
			t.Fatal(e)
		}
		if len(load("seed", id).Channels) != 2 {
			t.Fatal("unreviewed prefix automatically accepted channel")
		}
		v = put(v)
		if e = s.SyncBillingUpstreamChannels(ctx, "seed", pending); e != nil {
			t.Fatal(e)
		}
		if len(load("seed", id).Channels) != 3 {
			t.Fatal("confirmed prefix failed to associate")
		}
		if len(v.ReviewPrefixes) != 0 || len(v.ChannelPrefixes) != 2 {
			t.Fatal("manual confirmation did not preserve and confirm aliases")
		}
	})
	t.Run("discovery provenance manual transfer and restoring auto", func(t *testing.T) {
		a := put(billing.Upstream{InstanceID: "rules", Name: "Vendor A", ChannelPrefixes: []string{"alpha", "alt"}, UpdatedBy: "admin"})
		b := put(billing.Upstream{InstanceID: "rules", Name: "Vendor B", ChannelPrefixes: []string{"alpha_cn"}, UpdatedBy: "admin"})
		source := []billing.ConfiguredChannel{{ChannelID: 1, ChannelName: "alpha_gpt", BaseURL: "https://gateway.example"}, {ChannelID: 2, ChannelName: "alpha_cn_gpt", BaseURL: "https://gateway.example"}, {ChannelID: 3, ChannelName: "brandnew_glm", BaseURL: "https://third.example"}, {ChannelID: 4, ChannelName: "alt", BaseURL: "https://alt.example"}}
		if e := s.SyncBillingUpstreamChannels(ctx, "rules", source); e != nil {
			t.Fatal(e)
		}
		a = load("rules", a.ID)
		b = load("rules", b.ID)
		if len(a.Channels) != 2 || len(b.Channels) != 1 || a.Channels[0].AssociationSource != "auto" || a.Channels[0].MatchedPrefix != "alpha" || a.Channels[0].AssociatedBy != "auto-channel" {
			t.Fatalf("auto rules: %+v %+v", a, b)
		}
		if len(a.URLs) != 2 || len(b.URLs) != 1 || b.URLs[0] != "https://gateway.example" {
			t.Fatal("shared endpoint missing")
		}
		revision := a.Revision
		if e := s.SyncBillingUpstreamChannels(ctx, "rules", source); e != nil {
			t.Fatal(e)
		}
		if load("rules", a.ID).Revision != revision {
			t.Fatal("no-op sync changes revision")
		}
		var discoveredID int64
		if e := db.QueryRow(`SELECT id FROM billing_upstreams WHERE instance_id='rules' AND name='brandnew'`).Scan(&discoveredID); e != nil {
			t.Fatal(e)
		}
		discovered := load("rules", discoveredID)
		if discovered.Enabled || discovered.Revision != 1 {
			t.Fatal("unsafe auto creation")
		}
		// Reassign an auto-created prefix without deleting its old subject or moving old channels.
		a.ChannelPrefixes = append(a.ChannelPrefixes, "brandnew")
		a.PrefixTransfers = []billing.UpstreamPrefixTransfer{{Prefix: "brandnew", FromUpstreamID: discoveredID}}
		a = put(a)
		old := load("rules", discoveredID)
		if len(old.ChannelPrefixes) != 0 || len(old.Channels) != 1 {
			t.Fatal("prefix transfer moved historical membership")
		}
		source = append(source, billing.ConfiguredChannel{ChannelID: 5, ChannelName: "brandnew_later"})
		if e := s.SyncBillingUpstreamChannels(ctx, "rules", source); e != nil {
			t.Fatal(e)
		}
		a = load("rules", a.ID)
		a.RemoveChannelIDs = []int64{1}
		a = put(a)
		if e := s.SyncBillingUpstreamChannels(ctx, "rules", source); e != nil {
			t.Fatal(e)
		}
		a = load("rules", a.ID)
		for _, c := range a.Channels {
			if c.ChannelID == 1 {
				t.Fatal("manual exclusion restored")
			}
		}
		excluded, e := s.BillingUpstreamChannelExclusions(ctx, "rules")
		if e != nil || len(excluded) != 1 || !excluded[0].AutoExcluded {
			t.Fatal("exclusions invisible", e)
		}
		if e = s.SyncBillingUpstreamChannelsWithRestore(ctx, "rules", source, []int64{1}, "reviewer"); e != nil {
			t.Fatal(e)
		}
		a = load("rules", a.ID)
		if len(a.Channels) != 3 {
			t.Fatalf("restored channels %+v", a.Channels)
		}
		// Atomic off-prefix transfer checks owner and does not migrate discount rows.
		if _, e = db.Exec(`INSERT INTO billing_discount_rules(instance_id,discount_type,subject_id,channel_id,model_name,discount,effective_from,remark,created_at,updated_at,updated_by) VALUES('rules','upstream_channel',?,1,'',0.8,'2025-01-01','',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),'admin')`, a.ID); e != nil {
			t.Fatal(e)
		}
		b = load("rules", b.ID)
		b.ChannelTransfers = []billing.UpstreamChannelTransfer{{ChannelID: 1, FromUpstreamID: a.ID}}
		b = put(b)
		if e = s.SyncBillingUpstreamChannels(ctx, "rules", source); e != nil {
			t.Fatal(e)
		}
		b = load("rules", b.ID)
		if len(b.Channels) != 2 || b.Channels[0].ChannelID != 1 || b.Channels[0].AssociationSource != "manual" || b.Channels[0].MatchedPrefix != "" {
			t.Fatalf("manual transfer lost: %+v", b)
		}
		var discountOwner int64
		if e = db.QueryRow(`SELECT subject_id FROM billing_discount_rules WHERE instance_id='rules' AND channel_id=1`).Scan(&discountOwner); e != nil || discountOwner != a.ID {
			t.Fatal("discount moved", e)
		}
		wrong := load("rules", a.ID)
		wrong.ChannelTransfers = []billing.UpstreamChannelTransfer{{ChannelID: 1, FromUpstreamID: discoveredID}}
		if _, e = s.PutBillingUpstream(ctx, wrong); !errors.Is(e, billing.ErrUpstreamTransferConflict) {
			t.Fatal("stale transfer accepted", e)
		}
		if e = s.DeleteBillingUpstream(ctx, "rules", a.ID); !errors.Is(e, billing.ErrUpstreamInUse) {
			t.Fatal("in-use upstream deleted", e)
		}
	})
	t.Run("case-insensitive display collision never enables prefixes", func(t *testing.T) {
		v := put(billing.Upstream{InstanceID: "case", Name: "FOO", ChannelPrefixes: []string{}})
		if e := s.SyncBillingUpstreamChannels(ctx, "case", []billing.ConfiguredChannel{{ChannelID: 10, ChannelName: "foo_gpt"}}); e != nil {
			t.Fatal(e)
		}
		v = load("case", v.ID)
		if len(v.ChannelPrefixes) != 0 || len(v.Channels) != 0 || v.Revision != 1 {
			t.Fatalf("display collision enabled rules: %+v", v)
		}
	})
	t.Run("optimistic concurrent editors and transaction rollback", func(t *testing.T) {
		v := put(billing.Upstream{InstanceID: "edit", Name: "original", URL: "https://old.example"})
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for _, name := range []string{"first", "second"} {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				draft := v
				draft.Name = name
				draft.URL = "https://" + name + ".example"
				_, e := s.PutBillingUpstream(ctx, draft)
				results <- e
			}(name)
		}
		wg.Wait()
		close(results)
		good, conflict := 0, 0
		for e := range results {
			if e == nil {
				good++
			} else if errors.Is(e, billing.ErrUpstreamRevisionConflict) {
				conflict++
			} else {
				t.Fatal(e)
			}
		}
		if good != 1 || conflict != 1 {
			t.Fatalf("lost update: success=%d conflicts=%d", good, conflict)
		}
		saved := load("edit", v.ID)
		if saved.Revision != 2 || len(saved.URLs) != 2 {
			t.Fatal("concurrent save lost aliases")
		}
		other := put(billing.Upstream{InstanceID: "edit", Name: "other", ChannelPrefixes: []string{"reserved"}})
		saved.ChannelPrefixes = []string{"reserved"}
		saved.URL = "https://rollback.example"
		if _, e := s.PutBillingUpstream(ctx, saved); !errors.Is(e, billing.ErrUpstreamPrefixConflict) {
			t.Fatal(e)
		}
		if len(load("edit", v.ID).URLs) != 2 || len(load("edit", other.ID).ChannelPrefixes) != 1 {
			t.Fatal("failed save did not roll back")
		}
	})
	t.Run("shared URL backfill stays per upstream and preserves models", func(t *testing.T) {
		a := put(billing.Upstream{InstanceID: "fill", Name: "first", Channels: []billing.UpstreamChannel{{ChannelID: 11, Models: []string{"keep"}}, {ChannelID: 12}, {ChannelID: 13}, {ChannelID: 14}}})
		b := put(billing.Upstream{InstanceID: "fill", Name: "second", Channels: []billing.UpstreamChannel{{ChannelID: 21}}})
		source := []billing.ConfiguredChannel{{ChannelID: 11, BaseURL: "HTTPS://shared.example:443/"}, {ChannelID: 12, BaseURL: "https://shared.example"}, {ChannelID: 13, BaseURL: ""}, {ChannelID: 21, BaseURL: "https://shared.example"}}
		report, e := s.BackfillBillingUpstreamURLs(ctx, "fill", source, false)
		if e != nil || len(report) != 2 || len(report[0].Added) != 1 || report[0].MissingChannels != 1 || report[0].InvalidURLs != 1 || len(report[1].Added) != 1 {
			t.Fatalf("preview: %+v %v", report, e)
		}
		if len(load("fill", a.ID).URLs) != 0 {
			t.Fatal("preview wrote")
		}
		if _, e = s.BackfillBillingUpstreamURLs(ctx, "fill", source, true); e != nil {
			t.Fatal(e)
		}
		a = load("fill", a.ID)
		b = load("fill", b.ID)
		if len(a.URLs) != 1 || len(b.URLs) != 1 || len(a.Channels[0].Models) != 1 || a.Revision != 2 {
			t.Fatal("backfill lost shared URL/models")
		}
		if _, e = s.BackfillBillingUpstreamURLs(ctx, "fill", source, true); e != nil {
			t.Fatal(e)
		}
		if load("fill", a.ID).Revision != 2 {
			t.Fatal("backfill not idempotent")
		}
	})

	t.Run("delete protects retained history after job cleanup", func(t *testing.T) {
		up := put(billing.Upstream{InstanceID: "retained-history", Name: "old subject", Channels: []billing.UpstreamChannel{{ChannelID: 1, ChannelName: "old"}}})
		day := time.Date(2026, 9, 1, 0, 0, 0, 0, billing.BusinessLocation)
		job, steps, e := billing.NewJob(up.InstanceID, day, day.AddDate(0, 0, 1), "test")
		if e != nil {
			t.Fatal(e)
		}
		job.JobType = "upstream_statement"
		job.UpstreamID = up.ID
		job.BillPeriod = "temporary"
		job.UsageVersion = 3
		job.RequestKey = job.ID
		job.MoneySnapshot, e = billing.NewMoneySnapshot(up.InstanceID, `{"QuotaPerUnit":500000}`, day)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.CreateBillingStatementJob(ctx, job, steps, up.Name); e != nil {
			t.Fatal(e)
		}
		up.RemoveChannelIDs = []int64{1}
		up = put(up)
		if _, e = db.Exec(`DELETE FROM billing_jobs WHERE id=?`, job.ID); e != nil {
			t.Fatal(e)
		}
		var n int
		if e = db.QueryRow(`SELECT COUNT(*) FROM billing_generation_tasks WHERE instance_id=?`, up.InstanceID).Scan(&n); e != nil || n != 1 {
			t.Fatal("task history fixture missing", n, e)
		}
		if e = s.DeleteBillingUpstream(ctx, up.InstanceID, up.ID); !errors.Is(e, billing.ErrUpstreamInUse) {
			t.Fatal("retained history lost subject", e)
		}
	})

	t.Run("pure config reads cache and immutable statement membership", func(t *testing.T) {
		v := put(billing.Upstream{InstanceID: "freeze", Name: "vendor", Channels: []billing.UpstreamChannel{{ChannelID: 1, ChannelName: "legacy", Models: []string{"keep"}}}})
		day := time.Date(2026, 10, 1, 0, 0, 0, 0, billing.BusinessLocation)
		job, steps, e := billing.NewJob("freeze", day, day.AddDate(0, 0, 1), "test")
		if e != nil {
			t.Fatal(e)
		}
		job.JobType = "upstream_statement"
		job.UpstreamID = v.ID
		job.RequestKey = job.ID
		job.MoneySnapshot, e = billing.NewMoneySnapshot("freeze", `{"QuotaPerUnit":"500000"}`, day)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.CreateBillingStatementJob(ctx, job, steps, v.Name); e != nil {
			t.Fatal(e)
		}
		source := &upstreamURLSource{channels: []billing.ConfiguredChannel{{ChannelID: 1, ChannelName: "legacy"}, {ChannelID: 2, ChannelName: "vendor_new", BaseURL: "https://new.example"}}}
		live := s.WithBillingUpstreamSource(source)
		if _, e = live.ListBillingUpstreamsConfig(ctx, "freeze"); e != nil {
			t.Fatal(e)
		}
		if source.calls != 0 {
			t.Fatal("config read contacted source")
		}
		if _, e = live.BillingStatementUpstream(ctx, "freeze", v.ID); e != nil {
			t.Fatal(e)
		}
		if _, e = live.BillingStatementUpstream(ctx, "freeze", v.ID); e != nil {
			t.Fatal(e)
		}
		if source.calls != 1 {
			t.Fatal("refresh cache ignored")
		}
		frozen, e := s.BillingStatementChannelIDs(ctx, job.ID)
		if e != nil || len(frozen) != 1 || !frozen[1] {
			t.Fatal("old statement changed", e)
		}
		if e = s.DeleteBillingUpstream(ctx, "freeze", v.ID); !errors.Is(e, billing.ErrUpstreamInUse) {
			t.Fatal("historical upstream deleted", e)
		}
		failing := s.WithBillingUpstreamSource(&upstreamURLSource{err: errors.New("offline")})
		if _, e = failing.ListBillingUpstreamsConfig(ctx, "freeze"); e != nil {
			t.Fatal("offline config failed")
		}
		if _, e = failing.BillingStatementUpstream(ctx, "freeze", v.ID); e == nil {
			t.Fatal("new billing ignored source failure")
		}
		empty := put(billing.Upstream{InstanceID: "empty", Name: "empty", URL: "https://unused.example"})
		if e = s.DeleteBillingUpstream(ctx, "empty", empty.ID); e != nil {
			t.Fatal(e)
		}
	})
}
