package mysqlstore

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLifelineCurrentQueryDeadline(t *testing.T) {
	s, site, _ := lifelineStore(t)
	s.db.SetMaxOpenConns(1)
	for name, read := range map[string]func(context.Context) error{
		"channels": func(ctx context.Context) error { _, e := s.latestChannels(ctx, site); return e },
		"policy":   func(ctx context.Context) error { _, _, e := s.getPolicy(ctx, site); return e },
		"bases":    func(ctx context.Context) error { _, e := s.listChannelBaseValues(ctx, site, ""); return e },
		"states":   func(ctx context.Context) error { _, e := s.listContinuousStates(ctx, site); return e },
		"current":  func(ctx context.Context) error { _, e := s.QueryLifelineCurrent(ctx, site); return e },
	} {
		t.Run(name, func(t *testing.T) {
			conn, err := s.db.Conn(context.Background())
			require.NoError(t, err)
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- read(ctx) }()
			select {
			case err := <-done:
				require.ErrorIs(t, err, context.DeadlineExceeded)
			case <-time.After(time.Second):
				conn.Close()
				<-done
				t.Fatal("read ignored request deadline while waiting for a connection")
			}
		})
	}
}

func TestLifelineCurrentLargeHistory(t *testing.T) {
	s, site, now := lifelineStore(t)
	ctx := context.Background()
	// A site with many finished rounds and expired pending reports must not
	// perform a correlated probe-history lookup for every historical command.
	for batch := 0; batch < 40; batch++ {
		args := []any{}
		probeArgs := []any{}
		for i := 0; i < 500; i++ {
			id := fmt.Sprintf("%s-%d", site, batch*500+i)
			args = append(args, id, site, now, now)
			probeArgs = append(probeArgs, fmt.Sprintf("%s%05d", site[5:], batch*500+i), site, now, now.Add(-time.Second), id)
		}
		_, err := s.db.ExecContext(ctx, `INSERT INTO channel_commands(id,instance_id,channel_id,command_type,payload_json,status,created_by,created_at,updated_at) VALUES `+strings.TrimSuffix(strings.Repeat("(?,?,7,'channel.probe','{}','succeeded','test',?,?),", 500), ","), args...)
		require.NoError(t, err)
		_, err = s.db.ExecContext(ctx, `INSERT INTO tuning_probe_records(id,instance_id,channel_id,channel_name,model_name,kind,started_at,deadline_at,outcome,reason,round_id,round_index) VALUES `+strings.TrimSuffix(strings.Repeat("(?,?,7,'test','m','recovery',?,?,'pending','',?,1),", 500), ","), probeArgs...)
		require.NoError(t, err)
	}
	agent := site + "-agent"
	_, err := s.db.ExecContext(ctx, `INSERT INTO instances(id,site_id,name,env,region,base_url,enabled,created_at) VALUES(?,?,'test','','','',1,?)`, agent, site, now)
	require.NoError(t, err)
	// Agent commands are scoped through instances; other-site commands must
	// never enter the current snapshot.
	id := site + "-active"
	_, err = s.db.ExecContext(ctx, `INSERT INTO channel_commands(id,instance_id,channel_id,command_type,payload_json,status,created_by,created_at,updated_at) VALUES(?,?,8,'channel.probe','{"model":"m","probe_count":3}','executing','test',?,?)`, id, agent, now, now)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	started := time.Now()
	active, err := s.currentRecoveryRounds(ctx, site, now, 7)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, id, active[8].ID)
	t.Logf("20,000 finished commands + expired pending records: %s", time.Since(started))
	var plan string
	err = s.db.QueryRowContext(ctx, "EXPLAIN FORMAT=JSON "+currentRecoveryRoundsSQL, site, site, site, now, now.Add(-7*24*time.Hour), site, site).Scan(&plan)
	require.NoError(t, err)
	require.Contains(t, plan, "idx_probe_pending", "pending requests must have an indexed path")
	other, err := s.currentRecoveryRounds(ctx, site+"-other", now, 7)
	require.NoError(t, err)
	require.Empty(t, other)
}
