package voicealert

import (
	"fmt"
	"math"
	"regexp"
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
type Config struct {
	Enabled          bool        `json:"enabled"`
	TtsCode          string      `json:"tts_code"`
	CalledShowNumber string      `json:"called_show_number"`
	Percent          float64     `json:"percent"`
	Delta            int64       `json:"delta"`
	Targets          []Target    `json:"targets"`
	Recipients       []Recipient `json:"recipients"`
}

func DefaultConfig() Config {
	return Config{Percent: 50, Delta: 10000000, Targets: []Target{}, Recipients: []Recipient{}}
}

var phonePattern = regexp.MustCompile(`^(1[3-9][0-9]{9}|0[0-9]{9,11})$`)

func (c Config) Validate() error {
	if math.IsNaN(c.Percent) || math.IsInf(c.Percent, 0) || c.Percent <= 0 || c.Percent > 10000 || c.Delta <= 0 || c.Delta > 1000000000000 {
		return fmt.Errorf("波动比例应在 0–10000%% 之间，差值阈值应在 1–1000000000000 之间")
	}
	if len(c.Targets) > 100 {
		return fmt.Errorf("最多配置 100 个客户")
	}
	if c.Enabled && (!strings.HasPrefix(c.TtsCode, "TTS_") || len(c.Targets) == 0 || len(c.Recipients) == 0) {
		return fmt.Errorf("启用前请填写已审核的 TTS 模板、值班接听号码和客户")
	}
	if len(c.TtsCode) > 128 || len(c.CalledShowNumber) > 128 {
		return fmt.Errorf("模板或显号过长")
	}
	seen := map[string]bool{}
	for _, t := range c.Targets {
		k := fmt.Sprintf("%s/%d", t.Site, t.UserID)
		if t.Site == "" || len(t.Site) > 64 || t.UserID <= 0 || t.UserID > 9007199254740991 || strings.TrimSpace(t.Label) == "" || len([]rune(t.Label)) > 40 {
			return fmt.Errorf("客户需填写站点 ID、有效用户 ID 及 40 字以内的 NewAPI 用户名")
		}
		if seen[k] {
			return fmt.Errorf("同一站点的客户不能重复配置")
		}
		seen[k] = true
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
			if !seen[key] {
				return fmt.Errorf("值班号码选择了不存在的客户")
			}
			if scopes[key] {
				return fmt.Errorf("同一值班号码的客户范围不能重复")
			}
			scopes[key] = true
		}
	}
	return nil
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
	if highAt > lowAt {
		direction = "上涨"
	}
	return delta > c.Delta && (low == 0 || float64(delta)/float64(low)*100 > c.Percent), low, high, direction
}
