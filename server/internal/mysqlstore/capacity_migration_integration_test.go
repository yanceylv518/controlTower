package mysqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"controltower/server/internal/tuning"
	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
)

// Uses a private schema because an upgrade must start with the real pre-096
// table. The isolated test account needs CREATE/DROP DATABASE privileges.
func TestCapacityMigrationStartupCompatibility(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires isolated CT_MYSQL_TEST_DSN")
	}
	cfg, err := mysql.ParseDSN(dsn)
	require.NoError(t, err)
	admin, err := OpenForMigrations(dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := fmt.Sprintf("ct_capacity_migration_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE DATABASE `" + schema + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci")
	require.NoError(t, err)
	defer func() {
		_, dropErr := admin.Exec("DROP DATABASE `" + schema + "`")
		require.NoError(t, dropErr)
	}()
	cfg.DBName = schema
	cfg.ParseTime = true
	db, err := OpenForMigrations(cfg.FormatDSN())
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)

	legacyDir, upgradeDir := t.TempDir(), t.TempDir()
	const version = "096_capacity_control.sql"
	paths, err := filepath.Glob("../../migrations/*.sql")
	require.NoError(t, err)
	for _, path := range paths {
		name := filepath.Base(path)
		if name > version {
			continue
		}
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(upgradeDir, name), data, 0600))
		if name < version {
			require.NoError(t, os.WriteFile(filepath.Join(legacyDir, name), data, 0600))
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	require.NoError(t, ApplyDir(ctx, db, legacyDir))
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, err = db.Exec(`INSERT INTO tuning_continuous_states
(instance_id,channel_id,model_name,proposed_weight,last_written_weight,last_write_at,phase,circuit_disabled,updated_at)
VALUES('upgrade',7,'model',75,80,?,'normal',0,?),('upgrade',8,'model',0,0,?,'circuit',1,?)`, now, now, now, now)
	require.NoError(t, err)

	t.Run("blocked DDL fails without marking the migration applied", func(t *testing.T) {
		blocker, err := OpenForMigrations(cfg.FormatDSN())
		require.NoError(t, err)
		defer blocker.Close()
		tx, err := blocker.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		var weight int
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT proposed_weight FROM tuning_continuous_states WHERE instance_id='upgrade' AND channel_id=7`).Scan(&weight))
		// Bound the test without changing the production 30-minute context.
		// The transaction retains a metadata lock until rollback.
		_, err = db.ExecContext(ctx, `SET SESSION lock_wait_timeout=1`)
		require.NoError(t, err)
		err = ApplyDir(ctx, db, upgradeDir)
		var mysqlErr *mysql.MySQLError
		require.ErrorAs(t, err, &mysqlErr)
		require.EqualValues(t, 1205, mysqlErr.Number)
		require.ErrorContains(t, err, "apply migration "+version)
		var count int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, version).Scan(&count))
		require.Zero(t, count)
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='tuning_continuous_states' AND column_name='capacity_control_json'`).Scan(&count))
		require.Zero(t, count)
		require.NoError(t, tx.Rollback())
	})
	if t.Failed() {
		return
	}

	t.Run("concurrent startup retries upgrade once and preserves old states", func(t *testing.T) {
		db.SetMaxOpenConns(2)
		started := time.Now()
		results := make(chan error, 2)
		for i := 0; i < 2; i++ {
			go func() { results <- ApplyDir(ctx, db, upgradeDir) }()
		}
		// Drain both workers before assertions/cleanup even if one failed.
		first, second := <-results, <-results
		require.NoError(t, first)
		require.NoError(t, second)
		t.Logf("concurrent 095 -> 096 migration completed in %s", time.Since(started))
		var dataType, nullable string
		var defaultValue sql.NullString
		require.NoError(t, db.QueryRow(`SELECT DATA_TYPE,IS_NULLABLE,COLUMN_DEFAULT FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='tuning_continuous_states' AND column_name='capacity_control_json'`).Scan(&dataType, &nullable, &defaultValue))
		require.Equal(t, "mediumtext", dataType)
		require.Equal(t, "YES", nullable)
		require.False(t, defaultValue.Valid)
		var nullCount int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM tuning_continuous_states WHERE capacity_control_json IS NULL`).Scan(&nullCount))
		require.Equal(t, 2, nullCount)
		states, err := New(db).ListContinuousStates("upgrade")
		require.NoError(t, err)
		require.Len(t, states, 2)
		byID := make(map[int64]tuning.ContinuousState)
		for _, state := range states {
			byID[state.ChannelID] = state
			require.Equal(t, tuning.CapacityControl{}, state.Capacity)
			require.Equal(t, now, state.LastWriteAt.UTC())
		}
		require.EqualValues(t, 75, byID[7].ProposedWeight)
		require.EqualValues(t, 80, *byID[7].LastWrittenWeight)
		require.Equal(t, "normal", byID[7].Phase)
		require.EqualValues(t, 0, byID[8].ProposedWeight)
		require.Equal(t, "circuit", byID[8].Phase)
		require.True(t, byID[8].CircuitDisabled)
	})
	if t.Failed() {
		return
	}

	t.Run("restart and missing version record preserve capacity evidence", func(t *testing.T) {
		states, err := New(db).ListContinuousStates("upgrade")
		require.NoError(t, err)
		state := states[0]
		state.Capacity = tuning.CapacityControl{Initialized: true, Active: true, Phase: "waiting_feedback", ConfirmedWeight: 75, AppliedAt: now, SampleAt: now}
		require.NoError(t, New(db).PutContinuousState(state))
		var appliedBefore, appliedAfter time.Time
		require.NoError(t, db.QueryRow(`SELECT applied_at FROM schema_migrations WHERE version=?`, version).Scan(&appliedBefore))
		require.NoError(t, ApplyDir(ctx, db, upgradeDir))
		require.NoError(t, db.QueryRow(`SELECT applied_at FROM schema_migrations WHERE version=?`, version).Scan(&appliedAfter))
		require.Equal(t, appliedBefore, appliedAfter)
		// Model a crash after ALTER succeeded but before the version INSERT.
		_, err = db.Exec(`DELETE FROM schema_migrations WHERE version=?`, version)
		require.NoError(t, err)
		require.NoError(t, ApplyDir(ctx, db, upgradeDir))
		var count int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, version).Scan(&count))
		require.Equal(t, 1, count)
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM tuning_continuous_states`).Scan(&count))
		require.Equal(t, 2, count)
		states, err = New(db).ListContinuousStates("upgrade")
		require.NoError(t, err)
		for _, restored := range states {
			if restored.ChannelID == state.ChannelID {
				require.Equal(t, state.Capacity, restored.Capacity)
			}
		}
	})
}
