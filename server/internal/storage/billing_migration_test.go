package storage

import (
	"os"
	"strings"
	"testing"
)

func TestBillingMigrationIsAdditiveAndComplete(t *testing.T) {
	data, err := os.ReadFile("../../migrations/025_billing.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	sql := strings.ToLower(string(data))
	for _, required := range []string{
		"create table if not exists billing_daily",
		"primary key (instance_id, user_id, model_name, group_name, tier_from, day)",
		"key idx_billing_daily_day (instance_id, day)",
		"create table if not exists billing_prices",
		"primary key (instance_id, model_name, effective_from, tier_from)",
		"create table if not exists billing_group_ratios",
		"primary key (instance_id, group_name)",
		"create table if not exists billing_ratio_snapshot",
		"primary key (instance_id, day)",
		"create table if not exists billing_balance_snapshot",
		"primary key (instance_id, user_id, day)",
		"decimal(12,6)",
		"decimal(8,4)",
		"engine=innodb",
		"default charset=utf8mb4",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("billing migration missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"drop table", "drop column", "truncate table", "delete from", "alter table",
	} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("billing migration must be forward-only; found %q", forbidden)
		}
	}
}
