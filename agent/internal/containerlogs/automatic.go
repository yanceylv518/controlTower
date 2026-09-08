package containerlogs

import (
	"context"
	cl "controltower/internal/containerlog"
	"time"
)

const automaticQueryTimeout = 10 * time.Minute

// One Server task owns the entire search. Reader pages and cursors are internal;
// progress uploads replace the same result and renew its lease.
type automaticQuery struct {
	releaseCursor string
	task          cl.Task
	query         cl.Query
	result        cl.Result
	bytes         int
	started       time.Time
}

func newAutomaticQuery(task cl.Task) *automaticQuery {
	return &automaticQuery{task: task, query: task.Query, result: cl.Result{Lines: []string{}}, started: time.Now()}
}
func (a *automaticQuery) step(ctx context.Context, read func(context.Context, cl.Query) cl.Result) cl.Result {
	deadline := a.started.Add(automaticQueryTimeout)
	if time.Now().After(deadline) {
		return a.timeout()
	}
	stepCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	page := read(stepCtx, a.query)
	a.releaseCursor = a.query.Cursor
	if page.NextCursor != "" {
		a.releaseCursor = page.NextCursor
	}
	a.result.ScannedBytes = page.ScannedBytes
	a.result.FilesScanned = page.FilesScanned
	a.result.TotalScannedBytes += page.ScannedBytes
	a.result.IndexedBytes = page.IndexedBytes
	a.result.Phase = page.Phase
	a.result.Truncated = a.result.Truncated || page.Truncated
	if stepCtx.Err() != nil && time.Now().After(deadline) {
		return a.timeout()
	}
	if page.Status != "succeeded" {
		a.result.Status = page.Status
		a.result.Error = page.Error
		a.result.Note = "查询未完成，已返回内容仅为部分结果。"
		a.result.Complete = false
		return a.result
	}
	capped := false
	for _, line := range page.Lines {
		if len(a.result.Lines) >= cl.MaxLines || a.bytes+len(line) > cl.MaxResultBytes {
			capped = true
			break
		}
		a.result.Lines = append(a.result.Lines, line)
		a.bytes += len(line)
	}
	if capped || (page.NextCursor != "" && (len(a.result.Lines) >= cl.MaxLines || a.bytes >= cl.MaxResultBytes)) {
		a.result.Status = "succeeded"
		a.result.Truncated = true
		a.result.Complete = false
		a.result.Note = "已达到返回条数或大小上限，结果不完整；请增加关键词或 Request ID 缩小匹配范围。"
		return a.result
	}
	if page.NextCursor != "" {
		if !cl.ValidSourceID(page.NextCursor) || page.NextCursor == a.query.Cursor {
			a.result.Status = "failed"
			a.result.Error = "日志读取服务未推进查询进度"
			return a.result
		}
		a.query.Cursor = page.NextCursor
		a.result.Status = "running"
		a.result.Complete = false
		a.result.Note = "后台正在自动处理下一批，无需重复提交查询。"
		if a.result.Truncated {
			a.result.Note += " 部分记录无法处理，结果可能不完整。"
		}
	} else {
		a.result.Status = "succeeded"
		a.result.Complete = page.Complete
		a.result.Note = page.Note
	}
	return a.result
}
func (a *automaticQuery) timeout() cl.Result {
	a.result.Status = "timed_out"
	a.result.Complete = false
	a.result.Error = "查询超过 10 分钟仍未完成"
	a.result.Note = "已返回内容仅为部分结果，请检查日志规模及读取服务负载。"
	return a.result
}
