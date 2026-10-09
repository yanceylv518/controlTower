package mysqlstore

import (
	"context"
	"controltower/server/internal/alblog"
	"controltower/server/internal/secrets"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestALBLogConfigPersistence(t *testing.T) {
	dsn := os.Getenv("CT_ALB_TEST_DSN")
	if dsn == "" {
		t.Skip("set CT_ALB_TEST_DSN to a disposable isolated MySQL database")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	sqlText, err := os.ReadFile("../../migrations/124_alb_access_log_config.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err = ApplySQL(ctx, db, string(sqlText)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow("SELECT COUNT(*) FROM alb_access_log_config").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("test requires empty ALB config table; existing configuration was not modified")
	}
	defer db.Exec("DELETE FROM alb_access_log_config WHERE id=1")
	s := New(db)
	if _, err = s.LoadALBLogConfig(ctx); !errors.Is(err, alblog.ErrMissing) {
		t.Fatal(err)
	}
	cipher, _ := secrets.Encrypt("synthetic-key", "synthetic-secret")
	c := alblog.Config{Endpoint: "cn-hangzhou.log.aliyuncs.com", Project: "test-project", Logstore: "alb-access-log", ALBID: "alb-test", AccessKeyID: "syntheticID", SecretCipher: cipher, LastTest: &alblog.Result{Status: "no_data"}}
	if err = s.SaveALBLogConfig(ctx, c, "tester"); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveALBLogConfig(ctx, c, "tester"); !errors.Is(err, alblog.ErrConflict) {
		t.Fatal("initial conflict not detected", err)
	}
	got, err := s.LoadALBLogConfig(ctx)
	if err != nil || got.Version != 1 || got.SecretCipher != cipher || !got.SecretSet || got.LastTest.Status != "no_data" {
		t.Fatal(got, err)
	}
	got.LastTest = &alblog.Result{Status: "failed", Code: "alb_auth_failed"}
	if err = s.SaveALBLogConfig(ctx, got, "tester"); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveALBLogConfig(ctx, got, "tester"); !errors.Is(err, alblog.ErrConflict) {
		t.Fatal("stale update accepted", err)
	}
	raw := ""
	if err = db.QueryRow("SELECT config_json FROM alb_access_log_config WHERE id=1").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "synthetic-secret") || strings.Contains(raw, "v1:") {
		t.Fatal("secret in public JSON")
	}
	got, err = s.LoadALBLogConfig(ctx)
	if err != nil || got.Version != 2 || got.LastTest.Code != "alb_auth_failed" {
		t.Fatal(got, err)
	}
}
