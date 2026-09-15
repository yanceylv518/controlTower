package logcollector

import "testing"

func TestAttemptMetadata(t *testing.T) {
	for _, tc := range []struct {
		other string
		want  int
	}{
		{`{"admin_info":{"use_channel":["7"]}}`, 1},
		{`{"admin_info":{"use_channel":[3,7]}}`, 2},
		{`{"admin_info":{"use_channel":["7","7"]}}`, 2},
		{`{"admin_info":{"use_channel":["7","3"]}}`, 0},
		{`{"admin_info":{"use_channel":[null,"7"]}}`, 0},
		{`{"admin_info":{"use_channel":[-1,7]}}`, 0},
		{`{"admin_info":{"use_channel":[7.2,7]}}`, 0},
		{`{"admin_info":{"use_channel":[]}}`, 0},
		{`{"admin_info":{"use_channel":"7"}}`, 0},
		{`{}`, 0}, {`not json`, 0},
	} {
		if got := attemptCount(tc.other, 7); got != tc.want {
			t.Fatalf("%s: got %d want %d", tc.other, got, tc.want)
		}
	}
}

func TestConvertRowCarriesAttemptEvidenceForStreamingTTFT(t *testing.T) {
	e, ok, err := ConvertRow(Row{Type: 2, ChannelID: 7, IsStream: true, Other: `{"frt":1000,"admin_info":{"use_channel":["3","7"]}}`})
	if err != nil || !ok || e.AttemptCount != 2 || e.FirstResponseMs == nil || *e.FirstResponseMs != 1000 {
		t.Fatalf("%+v %v", e, err)
	}
}
