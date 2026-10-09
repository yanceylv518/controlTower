package alblog

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

var ErrMissing = errors.New("alb_config_missing")
var ErrConflict = errors.New("alb_config_conflict")

type Config struct {
	Endpoint     string  `json:"endpoint"`
	Project      string  `json:"project"`
	Logstore     string  `json:"logstore"`
	ALBID        string  `json:"alb_id"`
	AccessKeyID  string  `json:"access_key_id"`
	SecretCipher string  `json:"-"`
	SecretSet    bool    `json:"secret_set"`
	Version      int64   `json:"version"`
	LastTest     *Result `json:"last_test,omitempty"`
}
type Result struct {
	Status        string `json:"status"`
	Code          string `json:"code,omitempty"`
	TestedAt      string `json:"tested_at"`
	From          int64  `json:"from"`
	To            int64  `json:"to"`
	RequestCount  *int64 `json:"request_count,omitempty"`
	LatestLogTime *int64 `json:"latest_log_time,omitempty"`
}
type Store interface {
	LoadALBLogConfig(context.Context) (Config, error)
	SaveALBLogConfig(context.Context, Config, string) error
}

var endpointPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}\.log\.aliyuncs\.com$`)
var projectPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,61}[a-z0-9]$`)
var logstorePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,62}[a-z0-9]$`)
var albPattern = regexp.MustCompile(`^alb-[a-zA-Z0-9]{1,60}$`)
var keyPattern = regexp.MustCompile(`^[a-zA-Z0-9]{8,128}$`)

func (c *Config) Normalize() error {
	c.Endpoint = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(c.Endpoint), "https://"), "/")
	c.Project = strings.TrimSpace(c.Project)
	c.Logstore = strings.TrimSpace(c.Logstore)
	c.ALBID = strings.TrimSpace(c.ALBID)
	c.AccessKeyID = strings.TrimSpace(c.AccessKeyID)
	c.Endpoint = strings.TrimPrefix(c.Endpoint, c.Project+".")
	if !endpointPattern.MatchString(c.Endpoint) || strings.Contains(c.Endpoint, "intranet") || !projectPattern.MatchString(c.Project) || !logstorePattern.MatchString(c.Logstore) || !albPattern.MatchString(c.ALBID) || !keyPattern.MatchString(c.AccessKeyID) || c.Version < 0 {
		return errors.New("alb_invalid_config")
	}
	return nil
}
func SameConnection(a, b Config) bool {
	return a.Endpoint == b.Endpoint && a.Project == b.Project && a.Logstore == b.Logstore && a.ALBID == b.ALBID && a.AccessKeyID == b.AccessKeyID && a.SecretCipher == b.SecretCipher
}
