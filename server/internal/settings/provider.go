package settings

import (
	"strings"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"controltower/server/internal/storage"
)

const (
	RetentionDetail      = "CT_RETENTION_DETAIL_DAYS"
	RetentionMetric5m    = "CT_RETENTION_METRIC5M_DAYS"
	RetentionRuntime     = "CT_RETENTION_RUNTIME_DAYS"
	RetentionAlerts      = "CT_RETENTION_ALERTS_DAYS"
	OfflineSeconds       = "CT_OFFLINE_ALERT_SECONDS"
	CPUWarn              = "CT_CPU_WARN_PERCENT"
	CPUCrit              = "CT_CPU_CRIT_PERCENT"
	MemoryWarn           = "CT_MEMORY_WARN_PERCENT"
	MemoryCrit           = "CT_MEMORY_CRIT_PERCENT"
	DiskWarn             = "CT_DISK_WARN_PERCENT"
	DiskCrit             = "CT_DISK_CRIT_PERCENT"
	ErrorRateWarn        = "CT_ERROR_RATE_WARN_PERCENT"
	ErrorRateCrit        = "CT_ERROR_RATE_CRIT_PERCENT"
	P95Warn              = "CT_P95_WARN_SECONDS"
	P95Crit              = "CT_P95_CRIT_SECONDS"
	NotificationsEnabled = "CT_NOTIFICATIONS_ENABLED"
	QuotaPerUnit         = "CT_QUOTA_PER_UNIT"
	CurrencySymbol       = "CT_CURRENCY_SYMBOL"
)

var defaults = map[string]string{
	RetentionDetail: "30", RetentionMetric5m: "90", RetentionRuntime: "7", RetentionAlerts: "30", OfflineSeconds: "120",
	CPUWarn: "80", CPUCrit: "90", MemoryWarn: "80", MemoryCrit: "90", DiskWarn: "85", DiskCrit: "95",
	ErrorRateWarn: "20", ErrorRateCrit: "50", P95Warn: "5", P95Crit: "10", NotificationsEnabled: "true",
	QuotaPerUnit: "500000", CurrencySymbol: "¥",
}

type Item struct {
	Value   string `json:"value"`
	Source  string `json:"source"`
	Default string `json:"default"`
}
type Values struct {
	RetentionDetailDays, RetentionMetric5mDays, RetentionRuntimeDays, RetentionAlertsDays, OfflineSeconds              int
	CPUWarn, CPUCrit, MemoryWarn, MemoryCrit, DiskWarn, DiskCrit, ErrorRateWarn, ErrorRateCrit, P95Warn, P95Crit float64
	NotificationsEnabled                                                                                         bool
}
type Provider struct {
	store  storage.SystemSettingStore
	ttl    time.Duration
	mu     sync.Mutex
	loaded time.Time
	items  map[string]Item
}

func NewProvider(store storage.SystemSettingStore, ttl time.Duration) *Provider {
	return &Provider{store: store, ttl: ttl}
}
// DefaultValue exposes the authoritative built-in default for a key so other
// packages never hand-copy the defaults map.
func DefaultValue(key string) string { return defaults[key] }

func Keys() []string {
	return []string{RetentionDetail, RetentionMetric5m, RetentionRuntime, RetentionAlerts, OfflineSeconds, CPUWarn, CPUCrit, MemoryWarn, MemoryCrit, DiskWarn, DiskCrit, ErrorRateWarn, ErrorRateCrit, P95Warn, P95Crit, NotificationsEnabled, QuotaPerUnit, CurrencySymbol}
}
func (p *Provider) Invalidate() { p.mu.Lock(); p.loaded = time.Time{}; p.mu.Unlock() }
func (p *Provider) Items() (map[string]Item, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.items != nil && time.Since(p.loaded) < p.ttl {
		return clone(p.items), nil
	}
	db, err := p.store.ListSystemSettings()
	if err != nil {
		return nil, err
	}
	stored := map[string]string{}
	for _, v := range db {
		stored[v.Key] = v.Value
	}
	items := map[string]Item{}
	for _, key := range Keys() {
		value, source := defaults[key], "default"
		if env, ok := os.LookupEnv(key); ok && env != "" {
			value, source = env, "env"
		}
		if v, ok := stored[key]; ok {
			value, source = v, "db"
		}
		items[key] = Item{Value: value, Source: source, Default: defaults[key]}
	}
	p.items = items
	p.loaded = time.Now()
	return clone(items), nil
}
func clone(in map[string]Item) map[string]Item {
	out := make(map[string]Item, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func (p *Provider) Current() (Values, error) {
	items, err := p.Items()
	if err != nil {
		return Values{}, err
	}
	return Parse(items)
}
func Parse(items map[string]Item) (Values, error) {
	i := func(k string) (int, error) { return strconv.Atoi(items[k].Value) }
	f := func(k string) (float64, error) { return strconv.ParseFloat(items[k].Value, 64) }
	var v Values
	var err error
	if v.RetentionDetailDays, err = i(RetentionDetail); err != nil {
		return v, err
	}
	if v.RetentionMetric5mDays, err = i(RetentionMetric5m); err != nil {
		return v, err
	}
	if v.RetentionRuntimeDays, err = i(RetentionRuntime); err != nil {
		return v, err
	}
	if v.RetentionAlertsDays, err = i(RetentionAlerts); err != nil {
		return v, err
	}
	if v.OfflineSeconds, err = i(OfflineSeconds); err != nil {
		return v, err
	}
	ptrs := []struct {
		k string
		p *float64
	}{{CPUWarn, &v.CPUWarn}, {CPUCrit, &v.CPUCrit}, {MemoryWarn, &v.MemoryWarn}, {MemoryCrit, &v.MemoryCrit}, {DiskWarn, &v.DiskWarn}, {DiskCrit, &v.DiskCrit}, {ErrorRateWarn, &v.ErrorRateWarn}, {ErrorRateCrit, &v.ErrorRateCrit}, {P95Warn, &v.P95Warn}, {P95Crit, &v.P95Crit}}
	for _, x := range ptrs {
		if *x.p, err = f(x.k); err != nil {
			return v, fmt.Errorf("%s: %w", x.k, err)
		}
	}
	v.NotificationsEnabled, err = strconv.ParseBool(items[NotificationsEnabled].Value)
	return v, err
}

func Validate(values map[string]string) map[string]string {
	errs := map[string]string{}
	known := map[string]bool{}
	for _, k := range Keys() {
		known[k] = true
	}
	for k := range values {
		if !known[k] {
			errs[k] = "unknown setting"
		}
	}
	get := func(k string) (float64, bool) {
		s, ok := values[k]
		if !ok {
			s = defaults[k]
		}
		v, e := strconv.ParseFloat(s, 64)
		if e != nil {
			errs[k] = "must be a number"
			return 0, false
		}
		return v, true
	}
	for _, k := range []string{RetentionDetail, RetentionMetric5m, RetentionRuntime, RetentionAlerts} {
		if v, ok := get(k); ok && (v < 1 || v > 365 || v != float64(int(v))) {
			errs[k] = "must be an integer between 1 and 365"
		}
	}
	if v, ok := get(OfflineSeconds); ok && (v < 1 || v != float64(int(v))) {
		errs[OfflineSeconds] = "must be a positive integer"
	}
	if v, ok := get(QuotaPerUnit); ok && (v < 1 || v > 1e9 || v != float64(int(v))) {
		errs[QuotaPerUnit] = "must be an integer between 1 and 1000000000"
	}
	if symbol, ok := values[CurrencySymbol]; ok {
		trimmed := strings.TrimSpace(symbol)
		if trimmed == "" || len([]rune(trimmed)) > 4 {
			errs[CurrencySymbol] = "must be 1-4 characters"
		}
	}
	for _, pair := range [][2]string{{CPUWarn, CPUCrit}, {MemoryWarn, MemoryCrit}, {DiskWarn, DiskCrit}, {ErrorRateWarn, ErrorRateCrit}} {
		w, wok := get(pair[0])
		c, cok := get(pair[1])
		if wok && (w < 1 || w > 100) {
			errs[pair[0]] = "must be between 1 and 100"
		}
		if cok && (c < 1 || c > 100) {
			errs[pair[1]] = "must be between 1 and 100"
		}
		if wok && cok && w >= c {
			errs[pair[0]] = "must be lower than critical"
		}
	}
	for _, k := range []string{P95Warn, P95Crit} {
		if v, ok := get(k); ok && (v < 0.5 || v > 600) {
			errs[k] = "must be between 0.5 and 600"
		}
	}
	w, wok := get(P95Warn)
	c, cok := get(P95Crit)
	if wok && cok && w >= c {
		errs[P95Warn] = "must be lower than critical"
	}
	if s, ok := values[NotificationsEnabled]; ok {
		if _, e := strconv.ParseBool(s); e != nil {
			errs[NotificationsEnabled] = "must be true or false"
		}
	}
	return errs
}
