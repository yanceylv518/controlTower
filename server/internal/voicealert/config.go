package voicealert

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

type Target struct {
	Site   string `json:"site"`
	UserID int64  `json:"user_id"`
	Label  string `json:"label"`
	Phone  string `json:"-"`
}
type Recipient struct {
	Phone   string   `json:"phone"`
	Targets []string `json:"targets"`
}
type DirectionRule struct {
	Enabled    bool    `json:"enabled"`
	UsePercent bool    `json:"use_percent"`
	Percent    float64 `json:"percent"`
	Delta      int64   `json:"delta"`
}
type Config struct {
	Rise             *DirectionRule `json:"rise,omitempty"`
	Fall             *DirectionRule `json:"fall,omitempty"`
	Enabled          bool           `json:"enabled"`
	TtsCode          string         `json:"tts_code"`
	CalledShowNumber string         `json:"called_show_number"`
	Percent          float64        `json:"percent"`
	UsePercent       bool           `json:"use_percent"`
	Delta            int64          `json:"delta"`
	Targets          []Target       `json:"targets,omitempty"` // Legacy input only; discovery uses the system directory.
	Recipients       []Recipient    `json:"recipients"`
}

func DefaultConfig() Config {
	return Config{UsePercent: true, Percent: 20, Delta: 10000000, Targets: []Target{}, Recipients: []Recipient{}}
}

// Missing direction rules inherit legacy thresholds; explicit disabled rules remain disabled.
func (c Config) Rule(direction string) DirectionRule {
	rule := c.Fall
	if direction == "上涨" {
		rule = c.Rise
	}
	if rule != nil {
		return *rule
	}
	return DirectionRule{Enabled: true, UsePercent: c.UsePercent, Percent: c.Percent, Delta: c.Delta}
}
func (c Config) WithDirectionRules() Config {
	rise, fall := c.Rule("上涨"), c.Rule("下降")
	c.Rise = &rise
	c.Fall = &fall
	return c
}

var phonePattern = regexp.MustCompile(`^(1[3-9][0-9]{9}|0[0-9]{9,11})$`)

func (c Config) Validate() error {
	for _, direction := range []string{"上涨", "下降"} {
		rule := c.Rule(direction)
		if math.IsNaN(rule.Percent) || math.IsInf(rule.Percent, 0) || rule.Percent <= 0 || rule.Percent > 10000 || rule.Delta <= 0 || rule.Delta > 1000000000000 {
			return fmt.Errorf("%s比例应在0–10000%%之间，差值应在1–1000000000000之间", direction)
		}
	}

	if math.IsNaN(c.Percent) || math.IsInf(c.Percent, 0) || c.Percent <= 0 || c.Percent > 10000 || c.Delta <= 0 || c.Delta > 1000000000000 {
		return fmt.Errorf("波动比例应在 0–10000%% 之间，差值阈值应在 1–1000000000000 之间")
	}
	if c.Enabled && (!strings.HasPrefix(c.TtsCode, "TTS_") || len(c.Recipients) == 0) {
		return fmt.Errorf("启用前请填写已审核的 TTS 模板和值班接听号码")
	}
	if len(c.TtsCode) > 128 || len(c.CalledShowNumber) > 128 {
		return fmt.Errorf("模板或显号过长")
	}
	phones := map[string]bool{}
	for _, recipient := range c.Recipients {
		if !phonePattern.MatchString(recipient.Phone) {
			return fmt.Errorf("值班接听号码需为国内手机或固话")
		}
		if phones[recipient.Phone] {
			return fmt.Errorf("值班接听号码不能重复")
		}
		phones[recipient.Phone] = true
		scopes := map[string]bool{}
		for _, key := range recipient.Targets {
			if _, _, ok := parseTargetKey(key); !ok {
				return fmt.Errorf("值班号码的客户范围格式无效，请从客户列表选择")
			}
			if scopes[key] {
				return fmt.Errorf("同一值班号码的客户范围不能重复")
			}
			scopes[key] = true
		}
	}
	return nil
}

func parseTargetKey(key string) (string, int64, bool) {
	site, user, ok := strings.Cut(key, "/")
	id, err := strconv.ParseInt(user, 10, 64)
	return site, id, ok && strings.TrimSpace(site) == site && site != "" && len(site) <= 64 && err == nil && id > 0 && id <= 9007199254740991 && strconv.FormatInt(id, 10) == user
}

func targetKey(t Target) string { return fmt.Sprintf("%s/%d", t.Site, t.UserID) }

func (r Recipient) Matches(t Target) bool {
	if len(r.Targets) == 0 {
		return true
	}
	want := targetKey(t)
	for _, key := range r.Targets {
		if key == want {
			return true
		}
	}
	return false
}
func Trigger(values []int64, c Config) (bool, int64, int64) {
	hit, low, high, _ := Evaluate(values, c)
	return hit, low, high
}

// Evaluate reports the direction of the most recent full excursion between
// the window minimum and maximum. Repeated extrema use their latest position.
func Evaluate(values []int64, c Config) (bool, int64, int64, string) {
	if len(values) != 11 {
		return false, 0, 0, ""
	}
	low, high := values[0], values[0]
	lowAt, highAt := 0, 0
	for i, v := range values {
		if v < 0 {
			return false, 0, 0, ""
		}
		if v <= low {
			low = v
			lowAt = i
		}
		if v >= high {
			high = v
			highAt = i
		}
	}
	delta := high - low
	direction := "下降"
	baseline := high
	if highAt > lowAt {
		direction = "上涨"
		baseline = low
	}
	// Measure against the value before the excursion, including declines.
	// A rise from zero has no finite ratio and uses the absolute threshold.
	rule := c.Rule(direction)
	return rule.Enabled && delta > rule.Delta && (!rule.UsePercent || baseline == 0 || float64(delta)/float64(baseline)*100 > rule.Percent), low, high, direction
}
