package dashboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"controltower/server/internal/storage"
	"controltower/server/internal/tuning"
	"github.com/stretchr/testify/require"
)

type statusUpdaterStub struct {
	calls, target int
	site          string
	channel       int64
	command       storage.ChannelCommand
	err           error
}

func (s *statusUpdaterStub) UpdateChannelStatus(_ context.Context, site string, channel int64, target int, _ string, _ time.Time) (storage.ChannelCommand, error) {
	s.calls++
	s.site, s.channel, s.target = site, channel, target
	return s.command, s.err
}

func TestManualChannelStatusHandler(t *testing.T) {
	for _, tc := range []struct {
		name, body, site, channel string
		exists                    bool
		err                       error
		command                   string
		want                      int
		code                      string
	}{
		{"close", `{"status":2,"confirm":true}`, "s", "7", true, nil, "cmd", 200, "succeeded"},
		{"open", `{"status":1,"confirm":true}`, "s", "7", true, nil, "cmd", 200, "succeeded"},
		{"unconfirmed", `{"status":2}`, "s", "7", true, nil, "", 400, "confirm_required"},
		{"invalid target", `{"status":3,"confirm":true}`, "s", "7", true, nil, "", 400, "invalid_channel_status"},
		{"no site", `{"status":2,"confirm":true}`, "", "7", true, nil, "", 400, "site_id_required"},
		{"invalid channel", `{"status":2,"confirm":true}`, "s", "-7", true, nil, "", 400, "invalid_channel_id"},
		{"other site channel", `{"status":2,"confirm":true}`, "s", "7", false, nil, "", 404, "channel_not_found"},
		{"busy", `{"status":2,"confirm":true}`, "s", "7", true, tuning.ErrChannelControlBusy, "", 409, "channel_control_busy"},
		{"no direct", `{"status":2,"confirm":true}`, "s", "7", true, tuning.ErrManualStatusUnavailable, "", 501, "channel_status_requires_direct_control"},
		{"held failure", `{"status":2,"confirm":true}`, "s", "7", true, errors.New("timeout"), "cmd", 502, "channel_status_unconfirmed_manual_hold"},
		{"before hold failure", `{"status":2,"confirm":true}`, "s", "7", true, errors.New("database"), "", 502, "channel_status_update_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := &statusUpdaterStub{command: storage.ChannelCommand{ID: tc.command, Status: "succeeded"}, err: tc.err}
			d := channelGroupDirectoryStub{}
			if tc.exists {
				d.channels = []tuning.Channel{{ID: 7, Status: "disabled"}}
			}
			r := httptest.NewRequest(http.MethodPut, "/api/dashboard/tuning/channels/7/status?site_id="+tc.site, strings.NewReader(tc.body))
			r.SetPathValue("channelID", tc.channel)
			w := httptest.NewRecorder()
			ChannelStatusHandler{Updater: u, Directory: d}.Update(w, r)
			require.Equal(t, tc.want, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), tc.code)
			if tc.want == 400 || tc.want == 404 {
				require.Zero(t, u.calls)
			}
			if tc.want == 200 {
				require.Equal(t, "s", u.site)
				require.Equal(t, int64(7), u.channel)
				require.Equal(t, 1, u.calls)
			}
		})
	}
}
