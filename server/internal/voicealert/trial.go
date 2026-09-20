package voicealert

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type TrialWatch struct {
	ID            string    `json:"id"`
	Site          string    `json:"site"`
	UserID        int64     `json:"user_id"`
	TokenID       int64     `json:"token_id"`
	Label         string    `json:"label"`
	Rule          string    `json:"rule"`
	GapMinutes    int       `json:"gap_minutes"`
	IncludeFailed bool      `json:"include_failed"`
	Phone         bool      `json:"phone"`
	Message       bool      `json:"message"`
	PersonIDs     []string  `json:"person_ids"`
	Enabled       bool      `json:"enabled"`
	Revision      int64     `json:"revision"`
	Round         int64     `json:"round"`
	StartedAt     time.Time `json:"started_at"`
	LastAt        time.Time `json:"last_at"`
	LastID        int64     `json:"last_id"`
	Fired         bool      `json:"fired"`
	Reset         bool      `json:"reset,omitempty"`
}
type TrialLog struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	TokenID   int64     `json:"token_id"`
	Type      int       `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	Model     string    `json:"model"`
}
type TrialIdentity struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}
type TrialSource interface {
	TrialHead(context.Context, string) (int64, error)
	TrialLogs(context.Context, string, int64) ([]TrialLog, error)
	TrialIdentities(context.Context, string, int64) ([]TrialIdentity, error)
}
type TrialEvent struct {
	ID         string          `json:"id"`
	Site       string          `json:"site"`
	SiteName   string          `json:"site_name"`
	WatchID    string          `json:"watch_id"`
	Customer   string          `json:"customer"`
	Log        TrialLog        `json:"log"`
	DetectedAt time.Time       `json:"detected_at"`
	FollowedBy string          `json:"followed_by"`
	Deliveries []TrialDelivery `json:"deliveries"`
}
type TrialDelivery struct {
	ID       string `json:"id"`
	PersonID string `json:"person_id"`
	Name     string `json:"name"`
	Phone    string `json:"phone"`
	Kind     string `json:"kind"`
	Status   string `json:"status"`
	Code     string `json:"code"`
}

func (w TrialWatch) Validate() error {
	if !validSite(w.Site) || w.UserID <= 0 || w.UserID > 9007199254740991 || w.TokenID < 0 || w.TokenID > 9007199254740991 {
		return fmt.Errorf("请选择当前站点的账户及 Key")
	}
	if strings.TrimSpace(w.Label) == "" || utf8.RuneCountInString(w.Label) > 80 {
		return fmt.Errorf("客户名称不能为空，最多80字")
	}
	if w.Rule != "first" && w.Rule != "resume" {
		return fmt.Errorf("触发规则无效")
	}
	if w.GapMinutes < 1 || w.GapMinutes > 10080 {
		return fmt.Errorf("静默时间应为1至10080分钟")
	}
	if len(w.PersonIDs) > 50 {
		return fmt.Errorf("通知人员最多50位")
	}
	if w.Enabled && (!w.Phone && !w.Message || w.Phone && len(w.PersonIDs) == 0) {
		return fmt.Errorf("请选择通知方式及电话接听人")
	}
	seen := map[string]bool{}
	for _, id := range w.PersonIDs {
		if len(id) != 32 || seen[id] {
			return fmt.Errorf("通知人员无效或重复")
		}
		seen[id] = true
	}
	return nil
}

// Replayed rows and historical requests cannot start a new round. A failed
// source query never advances this state or establishes a silence interval.
func (w *TrialWatch) Observe(l TrialLog) bool {
	if !w.Enabled || l.UserID != w.UserID || (w.TokenID > 0 && l.TokenID != w.TokenID) || l.ID <= w.LastID || l.CreatedAt.Before(w.StartedAt) || (l.Type != 2 && (!w.IncludeFailed || l.Type != 5)) {
		return false
	}
	hit := !w.Fired || (w.Rule == "resume" && !w.LastAt.IsZero() && l.CreatedAt.Sub(w.LastAt) >= time.Duration(w.GapMinutes)*time.Minute)
	w.LastID = l.ID
	if l.CreatedAt.After(w.LastAt) {
		w.LastAt = l.CreatedAt
	}
	if hit {
		w.Fired = true
	}
	return hit
}

func readWatches(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, site string) ([]TrialWatch, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,config_json,revision FROM trial_watches WHERE site_id=? ORDER BY id`, site)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrialWatch{}
	for rows.Next() {
		var w TrialWatch
		var raw, id string
		var revision int64
		if err = rows.Scan(&id, &raw, &revision); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &w); err != nil {
			return nil, err
		}
		w.ID = id
		w.Revision = revision
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s Store) SaveWatch(ctx context.Context, w TrialWatch, actor string, source TrialSource) (TrialWatch, error) {
	w.Label = strings.TrimSpace(w.Label)
	if err := w.Validate(); err != nil {
		return w, err
	}
	// Validate identity without ever accepting or fetching a full API key.
	var err error
	if w.Enabled || w.ID == "" {
		users, e := source.TrialIdentities(ctx, w.Site, 0)
		if e != nil {
			return w, fmt.Errorf("站点只读用户目录不可用")
		}
		found := false
		for _, u := range users {
			if u.ID == w.UserID {
				found = true
			}
		}
		if !found {
			return w, fmt.Errorf("账户不属于当前站点")
		}
		if w.TokenID > 0 {
			keys, e := source.TrialIdentities(ctx, w.Site, w.UserID)
			if e != nil {
				return w, fmt.Errorf("Key目录不可用")
			}
			found = false
			for _, k := range keys {
				if k.ID == w.TokenID {
					found = true
				}
			}
			if !found {
				return w, fmt.Errorf("Key不属于所选账户")
			}
		}
	}
	var head int64
	if w.Enabled {
		head, err = source.TrialHead(ctx, w.Site)
		if err != nil {
			return w, fmt.Errorf("无法连接站点日志库，不能开启检测")
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return w, err
	}
	defer tx.Rollback()
	if err = lockVoice(ctx, tx); err != nil {
		return w, err
	}
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM instances WHERE enabled=1 AND COALESCE(NULLIF(site_id,''),id)=?`, w.Site).Scan(&exists); err != nil {
		return w, err
	}
	if exists == 0 {
		return w, fmt.Errorf("站点不存在或已停用")
	}
	if w.Enabled && w.Phone {
		var raw string
		if err = tx.QueryRowContext(ctx, `SELECT config_json FROM voice_alert_site_config WHERE site_id=?`, w.Site).Scan(&raw); err != nil {
			return w, fmt.Errorf("请先保存本站点电话基础配置和测试模板")
		}
		var c Config
		if json.Unmarshal([]byte(raw), &c) != nil || !c.TrialTemplateReady || !strings.HasPrefix(c.TrialTtsCode, "TTS_") || c.ServiceEnabled != nil && !*c.ServiceEnabled {
			return w, fmt.Errorf("请启用本站点电话服务并配置已审核测试模板")
		}
	}
	for _, id := range w.PersonIDs {
		var active bool
		if err = tx.QueryRowContext(ctx, `SELECT enabled FROM operations_people WHERE id=?`, id).Scan(&active); err != nil {
			return w, fmt.Errorf("通知人员不存在")
		}
		if w.Enabled && w.Phone && !active {
			return w, fmt.Errorf("请移除已停用通知人员")
		}
	}
	now := time.Now().UTC()
	if w.ID == "" {
		w.ID, err = randomID(16)
		if err != nil {
			return w, err
		}
		w.Round = 1
		w.StartedAt = now.Truncate(time.Second)
		w.LastID = head
		w.LastAt = time.Time{}
		w.Fired = false
		w.Revision = 1
	} else {
		var raw string
		var revision int64
		if err = tx.QueryRowContext(ctx, `SELECT config_json,revision FROM trial_watches WHERE id=? AND site_id=?`, w.ID, w.Site).Scan(&raw, &revision); err != nil {
			return w, ErrConflict
		}
		if revision != w.Revision {
			return w, ErrConflict
		}
		var old TrialWatch
		if err = json.Unmarshal([]byte(raw), &old); err != nil {
			return w, err
		}
		if (old.UserID != w.UserID || old.TokenID != w.TokenID) && !w.Reset {
			return w, fmt.Errorf("变更监控对象请开启新一轮")
		}
		w.Round = old.Round
		w.StartedAt = old.StartedAt
		w.LastAt = old.LastAt
		w.LastID = old.LastID
		w.Fired = old.Fired
		if w.Reset {
			w.Round++
			w.Fired = false
			w.LastAt = time.Time{}
		}
		if w.Reset || (!old.Enabled && w.Enabled) {
			w.StartedAt = now.Truncate(time.Second)
			w.LastID = head
			w.LastAt = time.Time{}
		}
		w.Revision++
	}
	w.Reset = false
	raw, err := json.Marshal(w)
	if err != nil {
		return w, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO trial_watches(id,site_id,config_json,revision) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE config_json=VALUES(config_json),revision=VALUES(revision)`, w.ID, w.Site, string(raw), w.Revision)
	if err != nil {
		return w, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO trial_site_state(site_id,cursor_id,initialized) VALUES(?,?,?) ON DUPLICATE KEY UPDATE cursor_id=IF(initialized,cursor_id,VALUES(cursor_id)),initialized=initialized OR VALUES(initialized)`, w.Site, head, w.Enabled)
	if err != nil {
		return w, err
	}
	if err = auditTrial(ctx, tx, "trial_watch.configure", w.ID, actor); err != nil {
		return w, err
	}
	return w, tx.Commit()
}
