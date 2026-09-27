package dashboard

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-sql-driver/mysql"
)

type currencyConfigStore struct {
	unconfiguredReadonlyStore
	value string
}

func (s currencyConfigStore) ReadonlyDSNForSite(site string) (string, error) {
	if site == "configured" {
		return s.value, nil
	}
	return "", nil
}

func TestCurrencyHTTPReadsConfiguredSiteMySQL(t *testing.T) {
	dsn := os.Getenv("CT_ARCHIVE_TEST_DSN")
	if dsn == "" {
		t.Skip("CT_ARCHIVE_TEST_DSN required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = ""
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var suffix [8]byte
	if _, err = rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	name := "ct_currency_" + hex.EncodeToString(suffix[:])
	if _, err = db.Exec("CREATE DATABASE `" + name + "`"); err != nil {
		t.Fatal(err)
	}
	defer db.Exec("DROP DATABASE `" + name + "`")
	if _, err = db.Exec("CREATE TABLE `" + name + "`.options (`key` VARCHAR(100) PRIMARY KEY, value TEXT)"); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"QuotaPerUnit": "1000000", "USDExchangeRate": "6.987654321", "general_setting.quota_display_type": "CNY"} {
		if _, err = db.Exec("INSERT INTO `"+name+"`.options VALUES(?,?)", key, value); err != nil {
			t.Fatal(err)
		}
	}
	cfg.DBName = name
	encrypted, err := encryptSecret("local-test-key", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	h := &PassthroughHandler{Config: currencyConfigStore{value: encrypted}, SecretKey: "local-test-key"}
	defer func() {
		for _, pool := range h.pools {
			pool.db.Close()
		}
	}()
	for _, tc := range []struct {
		query  string
		status int
	}{
		{"site=configured", 200}, {"site_id=configured", 400}, {"site=unknown", 503},
	} {
		r := httptest.NewRequest("GET", "/api/dashboard/passthrough/currency?"+tc.query, nil)
		w := httptest.NewRecorder()
		h.Currency(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.query, w.Code, w.Body.String())
		}
		if w.Code == 200 {
			var result siteCurrency
			if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.SiteID != "configured" || result.ExchangeRate != "6.987654321" || result.RawQuotaPerUnit != "1000000" || result.Symbol != "¥" {
				t.Fatalf("%+v", result)
			}
		}
	}
}
