package voicealert

import (
	"context"
	ctauth "controltower/server/internal/auth"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type TrialHandler struct {
	Store         Store
	Source        TrialSource
	WorkerEnabled bool
}

func decodeTrial(w http.ResponseWriter, r *http.Request, v any) error {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return errors.New("invalid body")
	}
	return nil
}
func (h TrialHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	fail := func(code int, msg string) { w.WriteHeader(code); reply(map[string]string{"error": msg}) }
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	peoplePath := strings.HasSuffix(r.URL.Path, "operations-people")
	if peoplePath {
		if r.Method == http.MethodPut {
			var p Person
			if decodeTrial(w, r, &p) != nil {
				fail(400, "人员格式错误")
				return
			}
			saved, err := h.Store.SavePerson(ctx, p, ctauth.Actor(r))
			if err != nil {
				if errors.Is(err, ErrConflict) {
					fail(409, err.Error())
				} else {
					fail(400, err.Error())
				}
				return
			}
			reply(saved)
			return
		}
		if r.Method != http.MethodGet {
			fail(405, "method_not_allowed")
			return
		}
		items, err := h.Store.People(ctx)
		if err != nil {
			fail(500, "人员列表读取失败")
			return
		}
		reply(map[string]any{"items": items})
		return
	}
	site := r.URL.Query().Get("site_id")
	if !validSite(site) {
		fail(400, "请选择站点")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/identities") {
		if r.Method != http.MethodGet {
			fail(405, "method_not_allowed")
			return
		}
		var user int64
		var parseErr error
		if raw := r.URL.Query().Get("user_id"); raw != "" {
			user, parseErr = strconv.ParseInt(raw, 10, 64)
		}
		if parseErr != nil || user < 0 {
			fail(400, "用户无效")
			return
		}
		items, err := h.Source.TrialIdentities(ctx, site, user)
		if err != nil {
			fail(503, "只读账户或Key目录不可用，请检查站点只读数据库配置")
			return
		}
		reply(map[string]any{"items": items})
		return
	}
	if r.Method == http.MethodPut {
		var input struct {
			Action      string     `json:"action"`
			Watch       TrialWatch `json:"watch"`
			DisplayName string     `json:"display_name"`
			EventID     string     `json:"event_id"`
		}
		if decodeTrial(w, r, &input) != nil {
			fail(400, "请求格式错误")
			return
		}
		switch input.Action {
		case "watch":
			if input.Watch.Site != site {
				fail(400, "站点不匹配")
				return
			}
			item, err := h.Store.SaveWatch(ctx, input.Watch, ctauth.Actor(r), h.Source)
			if err != nil {
				if errors.Is(err, ErrConflict) {
					fail(409, err.Error())
				} else {
					fail(400, err.Error())
				}
				return
			}
			reply(item)
			return
		case "display_name":
			name := strings.TrimSpace(input.DisplayName)
			if utf8.RuneCountInString(name) > 80 {
				fail(400, "站点显示名称最多80字")
				return
			}
			tx, err := h.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				fail(500, "保存失败")
				return
			}
			defer tx.Rollback()
			if err = lockVoice(ctx, tx); err == nil {
				_, err = tx.ExecContext(ctx, `INSERT INTO trial_site_state(site_id,display_name) VALUES(?,?) ON DUPLICATE KEY UPDATE display_name=VALUES(display_name)`, site, name)
			}
			if err == nil {
				err = auditTrial(ctx, tx, "trial_site.rename", site, ctauth.Actor(r))
			}
			if err == nil {
				err = tx.Commit()
			}
			if err != nil {
				fail(500, "保存失败")
				return
			}
			reply(map[string]bool{"ok": true})
			return
		case "follow":
			tx, err := h.Store.DB.BeginTx(ctx, nil)
			if err != nil {
				fail(500, "关注失败")
				return
			}
			defer tx.Rollback()
			result, err := tx.ExecContext(ctx, `UPDATE trial_events SET followed_by=? WHERE id=? AND site_id=? AND followed_by=''`, ctauth.Actor(r), input.EventID, site)
			if err != nil {
				fail(500, "关注失败")
				return
			}
			n, _ := result.RowsAffected()
			if n == 0 {
				fail(409, "记录不存在或已关注")
				return
			}
			if err = auditTrial(ctx, tx, "trial_event.follow", input.EventID, ctauth.Actor(r)); err == nil {
				err = tx.Commit()
			}
			if err != nil {
				fail(500, "关注失败")
				return
			}
			reply(map[string]bool{"ok": true})
			return
		default:
			fail(400, "无效操作")
			return
		}
	}
	if r.Method != http.MethodGet {
		fail(405, "method_not_allowed")
		return
	}
	watches, err := readWatches(ctx, h.Store.DB, site)
	if err != nil {
		fail(500, "测试名单读取失败")
		return
	}
	people, err := h.Store.People(ctx)
	if err != nil {
		fail(500, "人员列表读取失败")
		return
	}
	for i := range people {
		people[i].Phone = maskPhone(people[i].Phone)
	}
	var alias, state string
	var checked sql.NullTime
	err = h.Store.DB.QueryRowContext(ctx, `SELECT display_name,state,checked_at FROM trial_site_state WHERE site_id=?`, site).Scan(&alias, &state, &checked)
	if err != nil && err != sql.ErrNoRows {
		fail(500, "检测状态读取失败")
		return
	}
	events, err := h.Store.TrialEvents(ctx, site)
	if err != nil {
		fail(500, "提醒记录读取失败")
		return
	}
	var siteName string
	if err = h.Store.DB.QueryRowContext(ctx, `SELECT COALESCE(NULLIF(MIN(name),''),?) FROM instances WHERE enabled=1 AND COALESCE(NULLIF(site_id,''),id)=?`, site, site).Scan(&siteName); err != nil {
		fail(500, "站点读取失败")
		return
	}
	phone, err := h.Store.Config(ctx, site)
	if err != nil {
		fail(500, "电话配置读取失败")
		return
	}
	phoneReady := phone.TrialTemplateReady && strings.HasPrefix(phone.TrialTtsCode, "TTS_") && (phone.ServiceEnabled == nil || *phone.ServiceEnabled)
	reply(map[string]any{"site_id": site, "site_name": siteName, "phone_ready": phoneReady, "watches": watches, "people": people, "events": events, "display_name": alias, "state": state, "checked_at": checked.Time, "worker_enabled": h.WorkerEnabled})
}
func maskPhone(s string) string {
	if len(s) > 7 {
		return s[:3] + "****" + s[len(s)-4:]
	}
	return s
}
func (s Store) TrialEvents(ctx context.Context, site string) ([]TrialEvent, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT payload_json,followed_by FROM trial_events WHERE site_id=? ORDER BY created_at DESC,id DESC LIMIT 100`, site)
	if err != nil {
		return nil, err
	}
	out := []TrialEvent{}
	for rows.Next() {
		var raw, by string
		if err = rows.Scan(&raw, &by); err != nil {
			rows.Close()
			return nil, err
		}
		var e TrialEvent
		if err = json.Unmarshal([]byte(raw), &e); err != nil {
			rows.Close()
			return nil, err
		}
		e.FollowedBy = by
		e.Deliveries = []TrialDelivery{}
		out = append(out, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		e := &out[i]
		rows, err = s.DB.QueryContext(ctx, `SELECT id,person_id,recipient_name,phone,kind,status,result_code FROM trial_deliveries WHERE site_id=? AND log_id=? ORDER BY id`, site, e.Log.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var d TrialDelivery
			if err = rows.Scan(&d.ID, &d.PersonID, &d.Name, &d.Phone, &d.Kind, &d.Status, &d.Code); err != nil {
				rows.Close()
				return nil, err
			}
			d.Phone = maskPhone(d.Phone)
			e.Deliveries = append(e.Deliveries, d)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
