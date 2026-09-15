package voicealert

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func validSite(site string) bool {
	return site != "" && len(site) <= 64 && strings.TrimSpace(site) == site && !strings.Contains(site, "/")
}
func (c Config) ValidateSite(site string) error {
	if !validSite(site) {
		return fmt.Errorf("请选择有效站点")
	}
	for _, r := range c.Recipients {
		for _, key := range r.Targets {
			targetSite, _, ok := parseTargetKey(key)
			if !ok || targetSite != site {
				return fmt.Errorf("客户范围只能包含当前站点")
			}
		}
	}
	return nil
}

// Preserve old settings as a disabled draft, never as active global defaults.
func legacyDraft(c Config, site string) Config {
	c.Enabled = false
	c.Targets = nil
	recipients := []Recipient{}
	for _, r := range c.Recipients {
		if len(r.Targets) == 0 {
			recipients = append(recipients, r)
			continue
		}
		selected := []string{}
		for _, key := range r.Targets {
			targetSite, _, ok := parseTargetKey(key)
			if ok && targetSite == site {
				selected = append(selected, key)
			}
		}
		if len(selected) > 0 {
			r.Targets = selected
			recipients = append(recipients, r)
		}
	}
	c.Recipients = recipients
	return c
}
func (s Store) legacyConfig(ctx context.Context, site string) (Config, error) {
	c := DefaultConfig()
	var raw string
	err := s.DB.QueryRowContext(ctx, "SELECT config_json FROM voice_alert_config WHERE id=1").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return c, err
	}
	c = legacyDraft(c, site)
	return c, c.Validate()
}
func (s Store) Configs(ctx context.Context) (map[string]Config, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT v.site_id,v.config_json FROM voice_alert_site_config v WHERE EXISTS(SELECT 1 FROM instances i WHERE i.enabled=1 AND CASE WHEN i.site_id='' THEN i.id ELSE i.site_id END=v.site_id)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Config{}
	for rows.Next() {
		var site, raw string
		if err := rows.Scan(&site, &raw); err != nil {
			return nil, err
		}
		c := DefaultConfig()
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, err
		}
		if err := c.Validate(); err != nil {
			return nil, err
		}
		if err := c.ValidateSite(site); err != nil {
			return nil, err
		}
		out[site] = c
	}
	return out, rows.Err()
}
