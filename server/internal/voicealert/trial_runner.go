package voicealert

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

type TrialCaller interface {
	Ready() bool
	CallTemplate(context.Context, Config, Target, string, map[string]string) Result
}
type TrialRunner struct {
	NotificationsAllowed func() (bool, error)
	Store                Store
	Source               TrialSource
	Caller               TrialCaller
	Message              func(context.Context, TrialEvent) (string, error)
}

func (r *TrialRunner) Run(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if err := r.Once(ctx); err != nil && ctx.Err() == nil {
			log.Print("trial follow-up pass failed; inspect database and source readiness")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (r *TrialRunner) Once(ctx context.Context) error {
	sites, err := r.Store.SiteIDs(ctx)
	if err != nil {
		return err
	}
	for _, site := range sites {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err = r.scanSite(ctx, site); err != nil {
			_, _ = r.Store.DB.ExecContext(ctx, `UPDATE trial_site_state SET state='source_unavailable',checked_at=? WHERE site_id=?`, time.Now().UTC(), site)
		}
	}
	return r.dispatch(ctx)
}
func (r *TrialRunner) scanSite(ctx context.Context, site string) error {
	var cursor int64
	var initialized bool
	err := r.Store.DB.QueryRowContext(ctx, `SELECT cursor_id,initialized FROM trial_site_state WHERE site_id=?`, site).Scan(&cursor, &initialized)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if !initialized {
		return nil
	}
	watches, err := readWatches(ctx, r.Store.DB, site)
	if err != nil {
		return err
	}
	active := false
	for _, w := range watches {
		if w.Enabled {
			active = true
		}
	}
	if !active {
		return nil
	}
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	logs, err := r.Source.TrialLogs(readCtx, site, cursor)
	cancel()
	if err != nil {
		return err
	}
	tx, err := r.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockVoice(ctx, tx); err != nil {
		return err
	}
	var current int64
	var alias string
	if err = tx.QueryRowContext(ctx, `SELECT cursor_id,display_name FROM trial_site_state WHERE site_id=?`, site).Scan(&current, &alias); err != nil {
		return err
	}
	if current != cursor {
		return nil
	}
	if alias == "" {
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(NULLIF(MIN(name),''),?) FROM instances WHERE enabled=1 AND COALESCE(NULLIF(site_id,''),id)=?`, site, site).Scan(&alias); err != nil {
			return err
		}
	}
	watches, err = readWatches(ctx, tx, site)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, l := range logs {
		if l.ID > current {
			current = l.ID
		}
		for i := range watches {
			w := &watches[i]
			if !w.Observe(l) {
				continue
			}
			eventID, e := randomID(16)
			if e != nil {
				return e
			}
			event := TrialEvent{ID: eventID, Site: site, SiteName: alias, WatchID: w.ID, Customer: w.Label, Log: l, DetectedAt: now}
			raw, e := json.Marshal(event)
			if e != nil {
				return e
			}
			_, e = tx.ExecContext(ctx, `INSERT IGNORE INTO trial_events(id,site_id,watch_id,round_id,log_id,payload_json,created_at) VALUES(?,?,?,?,?,?,?)`, event.ID, site, w.ID, w.Round, l.ID, string(raw), now)
			if e != nil {
				return e
			}
			if w.Phone {
				for _, person := range w.PersonIDs {
					var p Person
					e = tx.QueryRowContext(ctx, `SELECT name,phone,enabled FROM operations_people WHERE id=?`, person).Scan(&p.Name, &p.Phone, &p.Enabled)
					if e == sql.ErrNoRows {
						continue
					}
					if e != nil {
						return e
					}
					status := "pending"
					if !p.Enabled {
						status = "person_disabled"
					}
					if e = insertTrialDelivery(ctx, tx, event, person, p.Name, p.Phone, "phone", status); e != nil {
						return e
					}
				}
			}
			if w.Message {
				if e = insertTrialDelivery(ctx, tx, event, "", "", "", "message", "pending"); e != nil {
					return e
				}
			}
		}
	}
	for _, w := range watches {
		raw, e := json.Marshal(w)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE trial_watches SET config_json=? WHERE id=?`, string(raw), w.ID); e != nil {
			return e
		}
	}
	scanState := "healthy"
	if len(logs) >= 10000 {
		scanState = "catching_up"
	}
	_, err = tx.ExecContext(ctx, `UPDATE trial_site_state SET cursor_id=?,state=?,checked_at=? WHERE site_id=?`, current, scanState, now, site)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func insertTrialDelivery(ctx context.Context, tx *sql.Tx, e TrialEvent, person, name, phone, kind, status string) error {
	id, err := randomID(16)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT IGNORE INTO trial_deliveries(id,event_id,site_id,log_id,person_id,recipient_name,phone,kind,status,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, e.ID, e.Site, e.Log.ID, person, name, phone, kind, status, e.DetectedAt)
	return err
}

// Reserve once under the same guard and ledger as customer TPM calls. No
// retries after a process crash or an ambiguous external result.
func (r *TrialRunner) dispatch(ctx context.Context) error {
	rows, err := r.Store.DB.QueryContext(ctx, `SELECT id FROM trial_deliveries WHERE status='pending' ORDER BY created_at LIMIT 100`)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = r.deliver(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
func (r *TrialRunner) deliver(ctx context.Context, id string) error {
	tx, err := r.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockVoice(ctx, tx); err != nil {
		return err
	}
	var d TrialDelivery
	var raw, status string
	var created time.Time
	err = tx.QueryRowContext(ctx, `SELECT d.kind,d.person_id,d.phone,d.status,d.created_at,e.payload_json FROM trial_deliveries d JOIN trial_events e ON e.id=d.event_id WHERE d.id=?`, id).Scan(&d.Kind, &d.PersonID, &d.Phone, &status, &created, &raw)
	if err != nil {
		return err
	}
	if status != "pending" {
		return nil
	}
	var e TrialEvent
	if err = json.Unmarshal([]byte(raw), &e); err != nil {
		return err
	}
	now := time.Now().UTC()
	callID := ""
	status = "unknown"
	code := ""
	c := DefaultConfig()
	if r.NotificationsAllowed != nil {
		allowed, e := r.NotificationsAllowed()
		if e != nil {
			return e
		}
		if !allowed {
			status = "notifications_disabled"
		}
	}
	var enabled int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM instances WHERE enabled=1 AND COALESCE(NULLIF(site_id,''),id)=?`, e.Site).Scan(&enabled); err != nil {
		return err
	}
	if enabled == 0 {
		status = "site_disabled"
	} else if now.Sub(created) > 10*time.Minute {
		status = "expired"
	}
	var watchRaw string
	var eventRound int64
	if err = tx.QueryRowContext(ctx, `SELECT w.config_json,e.round_id FROM trial_events e JOIN trial_watches w ON w.id=e.watch_id WHERE e.id=?`, e.ID).Scan(&watchRaw, &eventRound); err != nil {
		return err
	}
	var currentWatch TrialWatch
	if err = json.Unmarshal([]byte(watchRaw), &currentWatch); err != nil {
		return err
	}
	if !currentWatch.Enabled || currentWatch.Round != eventRound {
		status = "watch_changed"
	}
	if d.Kind == "phone" && status == "unknown" {
		var active bool
		var currentPhone string
		err = tx.QueryRowContext(ctx, `SELECT phone,enabled FROM operations_people WHERE id=?`, d.PersonID).Scan(&currentPhone, &active)
		if err == sql.ErrNoRows || !active {
			status = "person_disabled"
		} else if err != nil {
			return err
		} else if currentPhone != d.Phone {
			status = "recipient_changed"
		}
		if status == "unknown" {
			err = tx.QueryRowContext(ctx, `SELECT config_json FROM voice_alert_site_config WHERE site_id=?`, e.Site).Scan(&raw)
			if err == sql.ErrNoRows {
				status = "template_unavailable"
			} else if err != nil {
				return err
			} else if err = json.Unmarshal([]byte(raw), &c); err != nil {
				return err
			}
			if c.ServiceEnabled != nil && !*c.ServiceEnabled {
				status = "service_disabled"
			} else if !c.TrialTemplateReady || c.TrialTtsCode == "" {
				status = "template_unavailable"
			} else if !r.Caller.Ready() {
				status = "credentials_missing"
			}
		}
		if status == "unknown" {
			var minute, hour, day int
			err = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(created_at>?),0),COALESCE(SUM(created_at>?),0),COUNT(*) FROM voice_alert_calls WHERE phone=? AND created_at>?`, now.Add(-time.Minute), now.Add(-time.Hour), d.Phone, now.Add(-24*time.Hour)).Scan(&minute, &hour, &day)
			if err != nil {
				return err
			}
			if minute >= 1 || hour >= 5 || day >= 20 {
				status = "limited"
			} else {
				callID, err = randomID(7)
				if err != nil {
					return err
				}
				_, err = tx.ExecContext(ctx, `INSERT INTO voice_alert_calls(id,site_id,user_id,phone,window_end,min_tpm,max_tpm,direction,status,created_at,suppress_until) VALUES(?,?,?,?,?,0,0,'测试开始','unknown',?,?)`, callID, e.Site, e.Log.UserID, d.Phone, e.Log.CreatedAt, now, now)
				if err != nil {
					return err
				}
			}
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE trial_deliveries SET status=?,call_id=? WHERE id=?`, status, callID, id)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if status != "unknown" {
		return nil
	}
	if d.Kind == "phone" {
		c.TtsCode = c.TrialTtsCode
		result := r.Caller.CallTemplate(ctx, c, Target{Site: e.Site, UserID: e.Log.UserID, Label: e.Customer, Phone: d.Phone}, callID, map[string]string{"site": e.SiteName, "customer": e.Customer})
		status = result.Status
		code = result.Code
		finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err = r.Store.Finish(finishCtx, callID, result, now); err != nil {
			return err
		}
	} else if r.Message == nil {
		status = "no_channel"
	} else {
		status, err = r.Message(ctx, e)
		if err != nil {
			status = "unknown"
			code = "message_failed"
		}
	}
	finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = r.Store.DB.ExecContext(finishCtx, `UPDATE trial_deliveries SET status=?,result_code=? WHERE id=?`, status, code, id)
	return err
}

func trialSummary(e TrialEvent) string {
	return fmt.Sprintf("%s · %s · user #%d · Key #%d", e.SiteName, e.Customer, e.Log.UserID, e.Log.TokenID)
}
