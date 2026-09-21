package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// MoneySnapshot is an observation at bill creation, not evidence of historical
// effective dates. Display currency never changes the underlying USD charge.
type MoneySnapshot struct {
	ID           string          `json:"id"`
	SiteID       string          `json:"site_id"`
	ObservedAt   time.Time       `json:"observed_at"`
	QuotaPerUnit string          `json:"quota_per_unit"`
	BaseCurrency string          `json:"base_currency"`
	Display      CurrencyDisplay `json:"display"`
	Policy       string          `json:"policy"`
	Evidence     string          `json:"evidence"`
}

type MoneySnapshotStore interface {
	BillingJobMoneySnapshot(context.Context, string) (*MoneySnapshot, error)
}

// NewMoneySnapshot retains only monetary options; model/group ratios must not
// be applied a second time to the already charged quota.
func NewMoneySnapshot(site, raw string, observed time.Time) (*MoneySnapshot, error) {
	var options map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &options); err != nil {
		return nil, err
	}
	allowed := map[string]json.RawMessage{}
	for _, key := range []string{"QuotaPerUnit", "USDExchangeRate", "DisplayInCurrencyEnabled", "general_setting", "general_setting.quota_display_type", "general_setting.custom_currency_symbol", "general_setting.custom_currency_exchange_rate", "ct.quota_per_unit_source"} {
		if value, ok := options[key]; ok {
			allowed[key] = value
		}
	}
	displayType := allowed["general_setting.quota_display_type"]
	if blob, ok := allowed["general_setting"]; ok {
		var encoded string
		var settings map[string]json.RawMessage
		if json.Unmarshal(blob, &encoded) != nil || json.Unmarshal([]byte(encoded), &settings) != nil {
			return nil, fmt.Errorf("invalid general_setting")
		}
		filtered := map[string]json.RawMessage{}
		for _, key := range []string{"quota_display_type", "custom_currency_symbol", "custom_currency_exchange_rate"} {
			if value, exists := settings[key]; exists {
				filtered[key] = value
			}
		}
		clean, _ := json.Marshal(filtered)
		allowed["general_setting"], _ = json.Marshal(string(clean))
		if len(displayType) == 0 {
			displayType = filtered["quota_display_type"]
		}
	}
	if len(displayType) > 0 {
		switch strings.ToUpper(strings.TrimSpace(rawJSONString(displayType))) {
		case "USD", "CNY", "CUSTOM", "TOKENS":
		default:
			return nil, fmt.Errorf("unsupported quota display type")
		}
	}
	evidence, err := json.Marshal(allowed)
	if err != nil {
		return nil, err
	}
	parsed, err := ParseRatioSnapshot(string(evidence))
	if err != nil {
		return nil, err
	}
	for _, value := range []string{parsed.QuotaPerUnit, parsed.Currency.ExchangeRate} {
		n, e := decimalRat(value)
		if e != nil || n.Sign() <= 0 {
			return nil, fmt.Errorf("invalid money snapshot unit or exchange rate")
		}
	}
	if site == "" || observed.IsZero() {
		return nil, fmt.Errorf("money snapshot identity missing")
	}
	s := &MoneySnapshot{SiteID: site, ObservedAt: observed.UTC().Truncate(time.Microsecond), QuotaPerUnit: parsed.QuotaPerUnit, BaseCurrency: "USD", Display: parsed.Currency, Policy: "observed_at_creation/v1;quota_div_unit;request_decimal12_half_away_from_zero", Evidence: string(evidence)}
	s.ID = s.digest()
	return s, nil
}

func (s MoneySnapshot) digest() string {
	s.ID = ""
	raw, _ := json.Marshal(s)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func (s MoneySnapshot) Validate(site string) error {
	if s.SiteID != site || s.ID != s.digest() {
		return fmt.Errorf("money snapshot identity or checksum mismatch")
	}
	copy, err := NewMoneySnapshot(s.SiteID, s.Evidence, s.ObservedAt)
	if err != nil {
		return err
	}
	if copy.ID != s.ID {
		return fmt.Errorf("money snapshot context mismatch")
	}
	return nil
}

func (r JobRunner) jobMoneySnapshot(ctx context.Context, job Job) (*MoneySnapshot, error) {
	if job.JobType != "user_statement" && job.JobType != "upstream_statement" {
		return nil, nil
	}
	if store, ok := r.Store.(MoneySnapshotStore); ok {
		s, err := store.BillingJobMoneySnapshot(ctx, job.ID)
		if err != nil {
			return nil, err
		}
		if s == nil {
			return nil, fmt.Errorf("billing_money_snapshot_missing: recreate unbound legacy task")
		}
		if err = s.Validate(job.InstanceID); err != nil {
			return nil, err
		}
		return s, nil
	}
	return nil, nil // Compatibility for nonpersistent adapters.
}
