package directcontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/channelschedule"
	"controltower/server/internal/tuning"
	"github.com/go-sql-driver/mysql"
)

type scheduleController interface {
	Read(context.Context, int64) (channelcontrol.Result, error)
	List(context.Context) ([]channelcontrol.Channel, error)
}

func (s Store) RunChannelSchedules(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		tickCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		plans, err := s.DueChannelSchedules(tickCtx, time.Now().UTC())
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("channel schedules: list failed: %v", err)
			}
		} else {
			// 同站点顺序检查，避免并行计划把彼此当作可用的替代渠道。
			groups := [][]channelschedule.Schedule{}
			bySite := map[string]int{}
			for _, plan := range plans {
				index, ok := bySite[plan.SiteID]
				if !ok {
					index = len(groups)
					bySite[plan.SiteID] = index
					groups = append(groups, nil)
				}
				groups[index] = append(groups[index], plan)
			}
			// 同一轮先开启替代渠道，再判断关闭覆盖；跨批次的开启由等待状态衔接。
			now := time.Now().UTC()
			for _, group := range groups {
				sort.SliceStable(group, func(i, j int) bool { return group[i].Position(now).Allowed && !group[j].Position(now).Allowed })
			}
			jobs := make(chan []channelschedule.Schedule)
			var workers sync.WaitGroup
			for i := 0; i < 4; i++ {
				workers.Add(1)
				go func() {
					defer workers.Done()
					for group := range jobs {
						for _, plan := range group {
							if tickCtx.Err() != nil {
								continue
							}
							if err := s.applyChannelSchedule(tickCtx, plan); err != nil && !errors.Is(err, channelschedule.ErrBusy) && !errors.Is(err, channelschedule.ErrSuperseded) && tickCtx.Err() == nil {
								log.Printf("channel schedules: site=%s channel=%d failed: %v", plan.SiteID, plan.ChannelID, err)
							}
						}
					}
				}()
			}
			for _, group := range groups {
				select {
				case jobs <- group:
				case <-tickCtx.Done():
				}
			}
			close(jobs)
			workers.Wait()
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s Store) applyChannelSchedule(parent context.Context, candidate channelschedule.Schedule) error {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	siteRelease, err := s.LockChannelScheduleSite(ctx, candidate.SiteID, 0)
	if err != nil {
		return err
	}
	defer siteRelease()
	release, err := s.LockChannelControl(ctx, candidate.SiteID, candidate.ChannelID)
	if err != nil {
		return err
	}
	defer release()
	plan, err := s.LoadChannelSchedule(ctx, candidate.SiteID, candidate.ChannelID)
	if err != nil {
		return err
	}
	if !plan.Enabled {
		return nil
	}
	now := time.Now().UTC()
	run, err := s.PrepareChannelSchedule(ctx, plan, now)
	if err != nil || run == nil {
		return err
	}
	if channelschedule.Terminal(run.Status) {
		return nil
	}
	finish := func(status, message string, written bool) error {
		// HTTP 结束后即使父任务取消，也给结果落盘一个有限的独立预算。
		recordCtx, recordCancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer recordCancel()
		return s.FinishChannelSchedule(recordCtx, *run, status, message, written, time.Now().UTC())
	}
	readFailure := func(cause error) error {
		if run.Status == "running" {
			return finish("unknown", "上次写入未确认，状态读取失败", false)
		}
		run.Attempts++
		status := "failed"
		if transientScheduleRead(cause) && run.Attempts < channelschedule.MaxAttempts {
			status = "retry"
		}
		return finish(status, "渠道状态或控制信息读取失败", false)
	}
	controller, direct, err := s.controllerForSite(plan.SiteID)
	if err != nil {
		return finish("failed", "读取直连配置失败", false)
	}
	if !direct {
		return finish("failed", "未配置直连控制", false)
	}
	reader, ok := controller.(scheduleController)
	if !ok {
		return finish("failed", "直连客户端不支持渠道状态读取", false)
	}
	if run.Status == "retry" && now.Sub(run.UpdatedAt) < time.Duration(1<<min(run.Attempts, 4))*30*time.Second {
		return nil
	}
	actual, err := reader.Read(ctx, plan.ChannelID)
	if err != nil {
		return readFailure(err)
	}
	if actual.Status == nil || (*actual.Status != 1 && *actual.Status != 2) {
		return finish("failed", "无法确认渠道开关状态", false)
	}
	if run.Status == "running" {
		if *actual.Status == run.Target {
			return finish("succeeded", "已回读确认状态", true)
		}
		return finish("unknown", "上次写入结果未确认", false)
	}
	run.BeforeStatus = *actual.Status
	if *actual.Status == run.Target {
		return finish("unchanged", "当前开关已符合计划", false)
	}
	if (run.Status == "waiting_recovery" || run.Status == "waiting_control") && now.Sub(run.Boundary) >= 10*time.Minute {
		return finish("failed", "超过计划时点10分钟仍未完成", false)
	}
	states, err := s.ListContinuousStates(plan.SiteID)
	if err != nil {
		return readFailure(err)
	}
	for _, state := range states {
		if state.ChannelID != plan.ChannelID {
			continue
		}
		if state.Capacity.PendingCommandID != "" || state.CircuitStatusCommandID != "" {
			return finish("waiting_control", "等待其他渠道操作完成", false)
		}
		if run.Target == 1 && (state.CircuitDisabled || state.Phase == "circuit" || state.Phase == "probing" || state.PausedReason == "write_failed") {
			policy, exists, policyErr := s.GetPolicy(plan.SiteID)
			if policyErr != nil {
				return readFailure(policyErr)
			}
			bases, baseErr := s.ListChannelBaseValues(plan.SiteID, state.ModelName)
			if baseErr != nil {
				return readFailure(baseErr)
			}
			canRecover := false
			for _, base := range bases {
				if base.ChannelID == plan.ChannelID && base.BaseWeight > 0 && len(base.Models) == 1 {
					canRecover = true
					break
				}
			}
			if !exists || policy.Policy.DispatchModes[state.ModelName] != "auto" || !canRecover {
				return finish("skipped", "熔断或写入异常，当前配置无法自动恢复", false)
			}
			return finish("waiting_recovery", "等待熔断或写入异常恢复", false)
		}
	}
	if run.Target == 2 {
		channels, err := reader.List(ctx)
		if err != nil {
			return readFailure(err)
		}
		if len(channels) > 5000 {
			return finish("skipped", "渠道数量超过检查上限", false)
		}
		controls, err := s.ListChannelScheduleControls(plan.SiteID, now)
		if err != nil {
			return readFailure(err)
		}
		if reason, waiting := scheduleCloseBlocked(plan.ChannelID, channels, states, controls); reason != "" {
			if waiting {
				return finish("waiting_control", "等待计划开启的替代渠道", false)
			}
			return finish("skipped", reason, false)
		}
	}
	// 长时间读目录后再核对边界，不能让旧的开启/关闭跨过下一时段执行。
	position := plan.Position(time.Now().UTC())
	target := 2
	if position.Allowed {
		target = 1
	}
	if target != run.Target || position.Boundary.After(run.Boundary) || run.ExpiresAt != nil && !time.Now().UTC().Before(*run.ExpiresAt) {
		return finish("superseded", "已进入下一计划时点", false)
	}
	available, configErr := s.HasDirectChannelControl(plan.SiteID)
	if configErr != nil {
		return readFailure(configErr)
	}
	if !available {
		return finish("failed", "站点已停用或直连配置已移除", false)
	}
	if err = s.BeginChannelScheduleWrite(ctx, run, *actual.Status, time.Now().UTC()); err != nil {
		return err
	}
	operation := "channel.schedule_close"
	if run.Target == 1 {
		operation = "channel.schedule_open"
	}
	tracked := &journalController{Controller: controller, journal: s.Store, site: plan.SiteID, actor: "system:schedule", operation: operation, before: map[string]any{"status": *actual.Status}}
	_, err = tracked.Update(ctx, channelcontrol.UpdateRequest{ChannelID: plan.ChannelID, Status: &run.Target})
	tracked.link(run.ID)
	if err != nil {
		if errors.Is(err, channelcontrol.ErrStatusRejected) {
			return finish("failed", "状态写入被拒绝，请检查直连权限及渠道配置", false)
		}
		// 网络错误可能发生在远端已写入之后，只读对账，不盲目覆盖人工操作。
		readCtx, readCancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer readCancel()
		observed, readErr := reader.Read(readCtx, plan.ChannelID)
		if readErr == nil && observed.Status != nil && *observed.Status == run.Target {
			return finish("succeeded", "已回读确认状态", true)
		}
		return finish("unknown", "状态写入结果未确认", false)
	}
	return finish("succeeded", "", true)
}

func transientScheduleRead(err error) bool {
	var network net.Error
	var response *channelcontrol.HTTPError
	var database *mysql.MySQLError
	return errors.Is(err, channelcontrol.ErrListChanged) || errors.As(err, &network) && network.Timeout() || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.As(err, &response) && (response.StatusCode == 429 || response.StatusCode >= 500) ||
		errors.As(err, &database) && (database.Number == 1205 || database.Number == 1213)
}

func scheduleCloseBlocked(channelID int64, channels []channelcontrol.Channel, states []tuning.ContinuousState, controls map[int64]channelschedule.Control) (string, bool) {
	var target *channelcontrol.Channel
	for i := range channels {
		if channels[i].ID == channelID {
			target = &channels[i]
			break
		}
	}
	if target == nil {
		return "渠道不存在", false
	}
	blockedChannels := map[int64]bool{}
	for _, state := range states {
		if state.CircuitDisabled || state.Capacity.PendingCommandID != "" || state.CircuitStatusTarget != 0 || state.PausedReason != "" || state.Phase != "" && state.Phase != "normal" {
			blockedChannels[state.ChannelID] = true
		}
	}
	models := splitScheduleValues(target.Models)
	groups := splitScheduleValues(target.Group)
	if len(models) == 0 || len(groups) == 0 {
		return "无法确认模型或分组覆盖", false
	}
	waitingReason := ""
	for _, model := range models {
		for _, group := range groups {
			covered := false
			pending := false
			for _, peer := range channels {
				if peer.ID == channelID || !containsScheduleValue(peer.Models, model) || !containsScheduleValue(peer.Group, group) {
					continue
				}
				control := controls[peer.ID]
				if control.Enabled && !control.Allowed {
					continue
				}
				pending = pending || control.Opening
				if peer.Status != 1 || peer.Weight == 0 || blockedChannels[peer.ID] {
					continue
				}
				covered = true
				break
			}
			if !covered {
				reason := fmt.Sprintf("模型 %s / 分组 %s 缺少可确认的替代渠道", model, group)
				if !pending {
					return reason, false
				}
				waitingReason = reason
			}
		}
	}
	return waitingReason, waitingReason != ""
}

func splitScheduleValues(value string) []string {
	var out []string
	for _, v := range strings.Split(value, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
func containsScheduleValue(value, want string) bool {
	for _, v := range splitScheduleValues(value) {
		if v == want {
			return true
		}
	}
	return false
}
