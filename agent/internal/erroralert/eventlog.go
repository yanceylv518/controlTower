package erroralert

import (
	"encoding/json"
	"os"
	"time"
)

const eventLogMaxBytes int64 = 5 * 1024 * 1024

type EventRecord struct {
	Time         time.Time `json:"time"`
	Dimension    string    `json:"dimension"`
	Label        string    `json:"label"`
	Rule         string    `json:"rule"`
	Kind         string    `json:"kind"`
	WindowCount  int       `json:"window_count"`
	Threshold    int       `json:"threshold"`
	EpisodeStart time.Time `json:"episode_start,omitempty"`
	EpisodeTotal int       `json:"episode_total,omitempty"`
}

type eventLogger struct {
	path   string
	logf   func(string, ...any)
	failed bool
}

func newEventLogger(path string, logf func(string, ...any)) *eventLogger {
	return &eventLogger{path: path, logf: logf}
}
func (l *eventLogger) append(records []EventRecord) {
	if l == nil || l.failed || l.path == "" || len(records) == 0 {
		return
	}
	if info, err := os.Stat(l.path); err == nil && info.Size() > eventLogMaxBytes {
		_ = os.Remove(l.path + ".1")
		if err := os.Rename(l.path, l.path+".1"); err != nil {
			l.fail(err)
			return
		}
	} else if err != nil && !os.IsNotExist(err) {
		l.fail(err)
		return
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		l.fail(err)
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, record := range records {
		if err := enc.Encode(record); err != nil {
			l.fail(err)
			return
		}
	}
}
func (l *eventLogger) fail(err error) {
	l.failed = true
	l.logf("control tower alert event log disabled after write failure: %v", err)
}
