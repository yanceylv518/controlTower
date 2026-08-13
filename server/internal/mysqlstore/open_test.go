package mysqlstore

import (
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func TestDatabaseTimeoutDefaults(t *testing.T) {
	dsn, err := withDatabaseTimeoutDefaults("user:pass@tcp(mysql:3306)/ct?parseTime=true")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Timeout != 5*time.Second || cfg.ReadTimeout != 30*time.Second || cfg.WriteTimeout != 30*time.Second {
		t.Fatalf("unexpected defaults: connect=%v read=%v write=%v", cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout)
	}
}

func TestDatabaseTimeoutDefaultsPreserveExplicitValues(t *testing.T) {
	dsn, err := withDatabaseTimeoutDefaults("user:pass@tcp(mysql:3306)/ct?timeout=2s&readTimeout=3s&writeTimeout=4s")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Timeout != 2*time.Second || cfg.ReadTimeout != 3*time.Second || cfg.WriteTimeout != 4*time.Second {
		t.Fatalf("explicit values changed: connect=%v read=%v write=%v", cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout)
	}
}
