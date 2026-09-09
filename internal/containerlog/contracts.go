// Package containerlog defines the fixed, read-only container log query protocol.
package containerlog

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxResultBytes = 512 * 1024
const MaxLines = 2000

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
var valuePattern = regexp.MustCompile(`^[a-zA-Z0-9_.:-]{1,128}$`)

var sourcePattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func ValidSourceID(s string) bool { return sourcePattern.MatchString(s) }

func ValidName(s string) bool { return namePattern.MatchString(s) }

type Query struct {
	Kind          string    `json:"kind,omitempty"`
	Host          string    `json:"host,omitempty"`
	Path          string    `json:"path,omitempty"`
	MinDurationMS int       `json:"min_duration_ms,omitempty"`
	Level         string    `json:"level,omitempty"`
	BatchID       string    `json:"batch_id,omitempty"`
	Cursor        string    `json:"cursor,omitempty"`
	Keyword       string    `json:"keyword,omitempty"`
	SourceID      string    `json:"source_id,omitempty"`
	Container     string    `json:"container"`
	From          time.Time `json:"from"`
	To            time.Time `json:"to"`
	RequestID     string    `json:"request_id,omitempty"`
	ErrorCode     string    `json:"error_code,omitempty"`
}

func (q Query) Validate(now time.Time) error {
	if q.Kind != "" && q.Kind != "app" && q.Kind != "nginx_access" && q.Kind != "nginx_error" {
		return errors.New("invalid log kind")
	}
	if len(q.Host) > 253 || len(q.Path) > 256 || strings.ContainsAny(q.Host+q.Path, "\x00\r\n") || q.MinDurationMS < 0 || q.MinDurationMS > 3600000 {
		return errors.New("invalid nginx filter")
	}
	if q.Level != "" && !strings.Contains("|debug|info|notice|warn|error|crit|alert|emerg|", "|"+q.Level+"|") {
		return errors.New("invalid log level")
	}
	if q.BatchID != "" && !ValidName(q.BatchID) {
		return errors.New("invalid query batch")
	}
	if (q.Cursor != "" && !ValidSourceID(q.Cursor)) || !ValidSourceID(q.SourceID) || !ValidName(q.Container) || q.From.IsZero() || !q.To.After(q.From) || q.To.Sub(q.From) > time.Hour || q.To.After(now.Add(time.Minute)) || q.From.Before(now.Add(-3*24*time.Hour)) {
		return errors.New("invalid query range or container")
	}
	if utf8.RuneCountInString(q.Keyword) > 128 || strings.ContainsAny(q.Keyword, "\x00\r\n") {
		return errors.New("invalid keyword")
	}
	if (q.RequestID != "" && !valuePattern.MatchString(q.RequestID)) || (q.ErrorCode != "" && !valuePattern.MatchString(q.ErrorCode)) {
		return errors.New("invalid filter")
	}
	return nil
}

type Result struct {
	TotalScannedBytes int64    `json:"total_scanned_bytes,omitempty"`
	NextCursor        string   `json:"next_cursor,omitempty"`
	Complete          bool     `json:"complete"`
	Phase             string   `json:"phase,omitempty"`
	IndexedBytes      int64    `json:"indexed_bytes,omitempty"`
	Note              string   `json:"note,omitempty"`
	FilesScanned      int      `json:"files_scanned,omitempty"`
	ScannedBytes      int64    `json:"scanned_bytes,omitempty"`
	Status            string   `json:"status"`
	Lines             []string `json:"lines"`
	Truncated         bool     `json:"truncated"`
	Error             string   `json:"error,omitempty"`
}
type Task struct {
	ID         string    `json:"id"`
	InstanceID string    `json:"instance_id"`
	AgentID    string    `json:"agent_id"`
	ActorID    int64     `json:"actor_id"`
	Actor      string    `json:"actor"`
	ActorName  string    `json:"actor_name"`
	Query      Query     `json:"query"`
	Result     Result    `json:"result"`
	CreatedAt  time.Time `json:"created_at"`
}
type Target struct {
	Sources        []Source  `json:"sources"`
	DiscoveryError string    `json:"discovery_error,omitempty"`
	InstanceID     string    `json:"instance_id"`
	AgentID        string    `json:"agent_id"`
	Containers     []string  `json:"containers"`
	SeenAt         time.Time `json:"seen_at"`
}
type Poll struct {
	Sources        []Source `json:"sources"`
	DiscoveryError string   `json:"discovery_error,omitempty"`
	AgentID        string   `json:"agent_id"`
	Containers     []string `json:"containers"`
	TaskID         string   `json:"task_id,omitempty"`
	Result         *Result  `json:"result,omitempty"`
}

// Source never includes environment variables, credentials or arbitrary Docker metadata.
type Source struct {
	Kind        string   `json:"kind,omitempty"`
	Domains     []string `json:"domains,omitempty"`
	Fields      []string `json:"fields,omitempty"`
	QueryHost   string   `json:"query_host,omitempty"`
	FileName    string   `json:"-"`
	LogFormat   string   `json:"-"`
	Shared      bool     `json:"-"`
	ID          string   `json:"id"`
	Container   string   `json:"container"`
	ContainerID string   `json:"container_id"`
	LogDir      string   `json:"log_dir"`
	Timezone    string   `json:"timezone"`
	Available   bool     `json:"available"`
	Reason      string   `json:"reason,omitempty"`
	HostDir     string   `json:"-"`
}
type Inventory struct {
	Sources []Source `json:"sources"`
	Error   string   `json:"error,omitempty"`
}
