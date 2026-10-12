package tuning

import (
	"context"
	"strings"
	"time"
)

type LifelineChannel struct {
	CurrentRound *ProbeRound  `json:"current_round,omitempty"`
	ChannelID    int64        `json:"channel_id"`
	ChannelName  string       `json:"channel_name"`
	Models       []string     `json:"models"`
	Weight       int64        `json:"weight"`
	Priority     int64        `json:"priority"`
	Switch       string       `json:"switch"`
	State        string       `json:"state"`
	Reason       string       `json:"reason"`
	Next         string       `json:"next"`
	NextAt       *time.Time   `json:"next_at,omitempty"`
	ConfirmedAt  *time.Time   `json:"confirmed_at,omitempty"`
	Pending      int          `json:"pending"`
	Accelerated  int          `json:"accelerated"`
	Last         *ProbeRecord `json:"last,omitempty"`
	RoundID      *string      `json:"round_id,omitempty"`
}
type LifelineCurrent struct {
	QueriedAt time.Time         `json:"queried_at"`
	Channels  []LifelineChannel `json:"channels"`
}
type LifelineCurrentReader interface {
	QueryLifelineCurrent(context.Context, string) (LifelineCurrent, error)
}

func DescribeLifelineChannel(c Channel, b ChannelBaseValue, s ContinuousState, p PolicyRecord, r ProbeRuntime, now time.Time) LifelineChannel {
	v := LifelineChannel{ChannelID: c.ID, ChannelName: c.Name, Models: append([]string{}, c.Models...), Weight: c.Weight, Priority: c.Priority, Switch: "unknown", State: "unknown", Reason: "缺少有效的调度确认", Next: "等待新的调度状态", Last: r.Last}
	switch strings.ToLower(c.Status) {
	case "1", "enabled", "enable", "active", "normal":
		v.Switch = "enabled"
	case "2", "3", "disabled", "disable", "inactive", "auto_disabled", "auto-disabled":
		v.Switch = "disabled"
	}
	if s.CircuitDisabled && v.Switch == "disabled" && s.PausedReason != ManualDisabled && s.CircuitStatusTarget == 0 {
		v.Switch = "auto_disabled"
	}
	if s.LastWriteAt != nil && s.LastWrittenWeight != nil && s.LastWriteAt.After(b.SnapshotAt) {
		v.Weight = *s.LastWrittenWeight
	}
	fresh := !r.ConfirmedAt.IsZero() && now.Sub(r.ConfirmedAt) <= 20*time.Second && !r.ConfirmedAt.After(now.Add(time.Second))
	if v.Last != nil && v.Last.Outcome == "pending" && !v.Last.DeadlineAt.After(now) {
		last := *v.Last
		last.Outcome = "unconfirmed"
		v.Last = &last
	}
	if !r.ConfirmedAt.IsZero() {
		at := r.ConfirmedAt
		v.ConfirmedAt = &at
	}
	stop := func(reason, next string) LifelineChannel {
		v.State = "stopped"
		v.Reason = reason
		v.Next = next
		if fresh && r.Running {
			v.State = "stopping"
			v.Reason = reason + "，等待调度退出确认"
			v.Pending = r.Pending
		} else if r.Running {
			v.State = "unknown"
			v.Reason = reason + "，旧调度状态已过期，无法确认退出"
		}
		return v
	}
	if s.PausedReason == ManualDisabled {
		if v.Switch != "disabled" {
			v.Switch = "manual_hold"
		} else {
			v.Switch = "manual_disabled"
		}
		return stop("手动关闭保护生效", "不自动恢复；手动开启后重新判断资格")
	}
	if len(c.Models) != 1 {
		return stop("多模型渠道不参与自动探测", "可按模型查询历史记录")
	}
	if p.Policy.DispatchModes[c.Models[0]] != "auto" {
		return stop("模型未开启自动执行", "调整模型执行模式后重新判断资格")
	}
	// Circuit recovery is independent of the routine-probe toggle/selection.
	if s.Phase == "circuit" || s.Phase == "probing" || s.SoftStartPending || s.CircuitStatusTarget == 1 {
		at := s.UpdatedAt
		v.ConfirmedAt = &at
		if at.IsZero() || now.Sub(at) > 3*time.Minute {
			v.Reason = "恢复调度状态已过期"
			return v
		}
		v.NextAt = s.NextProbeAt
		v.RoundID = s.ProbeCommandID
		switch {
		case s.CircuitStatusTarget == 1 || s.SoftStartPending:
			v.State = "recovery"
			v.Reason = "恢复评估后，等待开启/软启动确认"
			v.Next = "以渠道开关确认结果为准"
		case s.Phase == "probing":
			v.State = "recovery"
			v.Reason = "恢复任务处理中，查看批次了解发送进度"
			v.Next = "检测结束后评估；通过后才尝试开启"
		default:
			v.State = "waiting"
			v.Reason = "熔断后等待恢复检测"
			v.Next = "到期后由调度器安排；不是已发送请求"
		}
		return v
	}
	if v.Switch == "disabled" || s.CircuitDisabled || s.RoutineCircuitLatched {
		return stop("渠道关闭或熔断保护中", "等待状态同步或恢复安排")
	}
	if v.Switch == "unknown" {
		v.Reason = "渠道开关状态未知"
		return v
	}
	if b.ChannelID == 0 {
		return stop("未配置调权基础值", "配置后重新判断资格")
	}
	if !p.Policy.Continuous.Rules().RoutineProbeEnabled {
		return stop("常态探针总开关关闭", "当前没有恢复任务")
	}
	if !p.Policy.Continuous.RoutineProbeEnabledFor(c.ID) {
		return stop("未加入常态探针范围", "当前没有恢复任务")
	}
	if b.BaseWeight <= 0 {
		return stop("基础权重为零", "基础权重调整后重新判断资格")
	}
	if !fresh || !r.Running || r.Revision != s.ControlRevision || !r.PolicyAt.Equal(p.UpdatedAt) || !r.BaseAt.Equal(b.UpdatedAt) || r.Model != c.Models[0] {
		v.Reason = "常态探针符合配置，但缺少新鲜调度确认"
		return v
	}
	v.State = "routine"
	v.Pending = r.Pending
	v.Accelerated = r.Accelerated
	v.NextAt = r.NextAt
	v.Reason = "周期等待，常态调度运行中"
	v.Next = "按周期发送下一次探针"
	if r.Pending > 0 {
		v.Reason = "请求执行中"
	}
	if r.Accelerated > 0 {
		v.Reason = "错误后加速确认中"
	}
	if s.Phase == "soft_start" {
		v.Reason += " · 恢复后观察"
	}
	return v
}
