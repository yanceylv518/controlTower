package dashboard

import (
	"context"
	"controltower/server/internal/tuning"
	"errors"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

type probeReadStub struct {
	*tuningStub
	query  tuning.ProbeRecordQuery
	source string
	err    error
}

func (s *probeReadStub) QueryProbeOverview(_ context.Context, q tuning.ProbeRecordQuery) (tuning.ProbeOverview, error) {
	s.query = q
	return tuning.ProbeOverview{RetentionDays: 7, Channels: []tuning.ProbeChannelStats{}}, s.err
}
func (s *probeReadStub) QueryProbeRecords(_ context.Context, q tuning.ProbeRecordQuery) (tuning.ProbeRecordPage, error) {
	s.query = q
	return tuning.ProbeRecordPage{Items: []tuning.ProbeRecord{}}, s.err
}
func (s *probeReadStub) QueryChannelLifeline(_ context.Context, q tuning.ProbeRecordQuery, source string) (tuning.ChannelLifePage, error) {
	s.query = q
	s.source = source
	return tuning.ChannelLifePage{Items: []tuning.ChannelLifeEvent{}}, s.err
}
func (s *probeReadStub) QueryLifelineCurrent(_ context.Context, site string) (tuning.LifelineCurrent, error) {
	s.query.Site = site
	return tuning.LifelineCurrent{Channels: []tuning.LifelineChannel{}}, s.err
}
func (s *probeReadStub) QueryProbeRounds(_ context.Context, q tuning.ProbeRecordQuery) (tuning.ProbeRoundPage, error) {
	s.query = q
	return tuning.ProbeRoundPage{Items: []tuning.ProbeRound{}}, s.err
}
func TestCurrentAndRoundsReadContracts(t *testing.T) {
	s := &probeReadStub{tuningStub: &tuningStub{}}
	h := NewHandler(nil).WithTuningStore(s)
	w := httptest.NewRecorder()
	h.HandleLifelineCurrent(w, httptest.NewRequest("GET", "/?site_id=site-b", nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, "site-b", s.query.Site)
	w = httptest.NewRecorder()
	h.HandleProbeRounds(w, httptest.NewRequest("GET", "/?site_id=site-b", nil))
	require.Equal(t, 400, w.Code)
	w = httptest.NewRecorder()
	h.HandleProbeRounds(w, httptest.NewRequest("GET", "/?site_id=site-b&channel_id=7&round_id=round-1", nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, "round-1", s.query.RoundID)
	require.EqualValues(t, 7, s.query.ChannelID)
	w = httptest.NewRecorder()
	h.HandleProbeRecords(w, httptest.NewRequest("GET", "/?site_id=site-b&dispatch=invalid", nil))
	require.Equal(t, 400, w.Code)
	w = httptest.NewRecorder()
	h.HandleChannelLifeline(w, httptest.NewRequest("GET", "/?site_id=site-b&channel_id=7&source=changes", nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, "changes", s.source)
	s.err = errors.New("private mysql details")
	w = httptest.NewRecorder()
	h.HandleLifelineCurrent(w, httptest.NewRequest("GET", "/?site_id=site-b", nil))
	require.Equal(t, 503, w.Code)
	require.NotContains(t, w.Body.String(), "private")
}
func TestProbeRecordsQueryValidationAndExplicitErrors(t *testing.T) {
	s := &probeReadStub{tuningStub: &tuningStub{}}
	h := NewHandler(nil).WithTuningStore(s)
	for _, query := range []string{"channel_id=-1", "limit=201", "from=not-a-date", "kind=bad", "outcome=slow|error", "cursor=broken", "from=2020-01-01T00:00:00Z&to=2022-01-01T00:00:00Z"} {
		w := httptest.NewRecorder()
		h.HandleProbeRecords(w, httptest.NewRequest("GET", "/?site_id=s&"+query, nil))
		require.Equal(t, 400, w.Code, query)
	}
	w := httptest.NewRecorder()
	h.HandleProbeRecords(w, httptest.NewRequest("GET", "/?site_id=s&channel_id=7&kind=scheduled&outcome=slow", nil))
	require.Equal(t, 200, w.Code)
	require.EqualValues(t, 7, s.query.ChannelID)
	require.Equal(t, "s", s.query.Site)
	w = httptest.NewRecorder()
	h.HandleChannelLifeline(w, httptest.NewRequest("GET", "/?site_id=s", nil))
	require.Equal(t, 400, w.Code)
	w = httptest.NewRecorder()
	h.HandleChannelLifeline(w, httptest.NewRequest("GET", "/?site_id=s&channel_id=7", nil))
	require.Equal(t, 200, w.Code)
	require.Equal(t, "important", s.source)
	s.err = errors.New("private mysql details")
	w = httptest.NewRecorder()
	h.HandleProbeOverview(w, httptest.NewRequest("GET", "/?site_id=s", nil))
	require.Equal(t, 503, w.Code)
	require.NotContains(t, w.Body.String(), "private")
}
