package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/channelschedule"
	"controltower/server/internal/storage"
)

type scheduleAPIStore struct {
	calls  int
	err    error
	config channelschedule.Config
}

func (f *scheduleAPIStore) ListChannelSchedules(context.Context, string) ([]channelschedule.Schedule, error) {
	f.calls++
	return []channelschedule.Schedule{}, f.err
}
func (f *scheduleAPIStore) SaveChannelSchedule(_ context.Context, _ string, _ int64, c channelschedule.Config, _ storage.OperationAudit, _ time.Time) (int64, error) {
	f.calls++
	f.config = c
	return c.Revision + 1, f.err
}
func (f *scheduleAPIStore) DeleteChannelSchedule(context.Context, string, int64, int64, *time.Time, storage.OperationAudit, time.Time) error {
	f.calls++
	return f.err
}
func (f *scheduleAPIStore) ChannelScheduleExecutions(context.Context, string, int64) ([]channelschedule.Execution, error) {
	f.calls++
	return []channelschedule.Execution{}, f.err
}
func (f *scheduleAPIStore) HasDirectChannelControl(string) (bool, error) { return true, f.err }

func TestChannelScheduleAPIValidationAndErrorMapping(t *testing.T) {
	valid := `{"enabled":true,"days":[7,1],"windows":[{"start":"22:00","end":"08:00"}],"revision":0,"align_now":false,"confirm":true}`
	for _, tc := range []struct {
		name, method, site, channel, body string
		err                               error
		code, calls                       int
	}{
		{"save", http.MethodPut, "site", "9", valid, nil, 200, 1},
		{"delete", http.MethodDelete, "site", "9", `{"revision":1,"confirm":true}`, nil, 200, 1},
		{"list", http.MethodGet, "site", "", "", nil, 200, 1},
		{"history", http.MethodGet, "site", "9", "", nil, 200, 1},
		{"no confirmation", http.MethodPut, "site", "9", strings.Replace(valid, `"confirm":true`, `"confirm":false`, 1), nil, 400, 0},
		{"unknown field", http.MethodPut, "site", "9", strings.TrimSuffix(valid, "}") + `,"extra":true}`, nil, 400, 0},
		{"trailing JSON", http.MethodPut, "site", "9", valid + `{}`, nil, 400, 0},
		{"invalid day", http.MethodPut, "site", "9", strings.Replace(valid, "[7,1]", "[0]", 1), nil, 400, 0},
		{"invalid window", http.MethodPut, "site", "9", strings.Replace(valid, "22:00", "24:00", 1), nil, 400, 0},
		{"zero channel", http.MethodPut, "site", "0", valid, nil, 400, 0},
		{"missing site", http.MethodPut, "", "9", valid, nil, 400, 0},
		{"zero delete revision", http.MethodDelete, "site", "9", `{"revision":0,"confirm":true}`, nil, 400, 0},
		{"conflict", http.MethodPut, "site", "9", valid, channelschedule.ErrConflict, 409, 1},
		{"busy", http.MethodPut, "site", "9", valid, channelschedule.ErrBusy, 409, 1},
		{"unsupported", http.MethodPut, "site", "9", valid, channelschedule.ErrUnsupported, 409, 1},
		{"limit", http.MethodPut, "site", "9", valid, channelschedule.ErrLimit, 409, 1},
		{"missing channel", http.MethodPut, "site", "9", valid, channelschedule.ErrChannelNotFound, 404, 1},
		{"storage failure", http.MethodPut, "site", "9", valid, errors.New("test failure"), 500, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &scheduleAPIStore{err: tc.err}
			handler := ChannelSchedulesHandler{Store: store, WorkerEnabled: true}
			r := httptest.NewRequest(tc.method, "/?site_id="+tc.site, strings.NewReader(tc.body))
			r.SetPathValue("channelID", tc.channel)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.code || store.calls != tc.calls {
				t.Fatalf("code=%d calls=%d body=%s", w.Code, store.calls, w.Body.String())
			}
			if tc.name == "save" && (store.config.Days[0] != 1 || store.config.Windows[0].Start != "22:00") {
				t.Fatalf("config not normalized: %+v", store.config)
			}
		})
	}
}

func TestChannelSchedulePreviewUsesScheduleAlgorithm(t *testing.T) {
	config := channelschedule.Config{Days: []int{1, 2, 3, 4, 5, 6, 7}, Windows: []channelschedule.Window{{Start: "00:00", End: "12:00"}, {Start: "12:00", End: "00:00"}}}
	raw, _ := json.Marshal(config)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	query := r.URL.Query()
	query.Set("config", string(raw))
	r.URL.RawQuery = query.Encode()
	w := httptest.NewRecorder()
	ChannelSchedulesHandler{}.HandlePreview(w, r)
	var preview channelschedule.Preview
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil || w.Code != 200 || !preview.Allowed || preview.NextAt != nil || len(preview.Windows) != 1 || preview.Windows[0].End != 10080 {
		t.Fatalf("preview=%+v body=%s err=%v", preview, w.Body.String(), err)
	}
}
