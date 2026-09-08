package agentgateway

import (
	"context"
	cl "controltower/internal/containerlog"
	"net/http"
)

type ContainerLogPoller interface {
	PollContainerLogs(context.Context, string, cl.Poll) (*cl.Task, error)
}

func (h Handler) ContainerLogs(store ContainerLogPoller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		instance, ok := h.authenticate(r)
		// Log access requires a per-instance token; legacy global tokens are not accepted.
		if !ok || instance == "" {
			writeError(w, 401, "instance_token_required")
			return
		}
		var p cl.Poll
		if decodeJSON(w, r, &p) != nil || !cl.ValidName(p.AgentID) || len(p.Sources) > 100 || len(p.DiscoveryError) > 1024 {
			writeError(w, 400, "invalid_query_poll")
			return
		}
		seen := map[string]bool{}
		for _, n := range p.Sources {
			if !cl.ValidName(n.Container) || seen[n.Container] || len(n.ContainerID) > 64 || len(n.LogDir) > 1024 || len(n.Timezone) > 128 || len(n.Reason) > 1024 || (n.Available && !cl.ValidSourceID(n.ID)) || len(n.ID) > 64 {
				writeError(w, 400, "invalid_container")
				return
			}
			seen[n.Container] = true
		}
		if p.Result != nil {
			if p.TaskID == "" || len(p.Result.Lines) > cl.MaxLines || len(p.Result.Error) > 256 || len(p.Result.Note) > 2048 || p.Result.FilesScanned < 0 || p.Result.FilesScanned > 256 || p.Result.ScannedBytes < 0 || p.Result.ScannedBytes > 64*1024*1024 || (p.Result.Status != "succeeded" && p.Result.Status != "failed" && p.Result.Status != "timed_out") {
				writeError(w, 400, "invalid_result")
				return
			}
			size := 0
			for _, line := range p.Result.Lines {
				size += len(line)
			}
			if size > cl.MaxResultBytes {
				writeError(w, 413, "result_too_large")
				return
			}
		}
		task, err := store.PollContainerLogs(r.Context(), instance, p)
		if err != nil {
			writeError(w, 500, "poll_failed")
			return
		}
		// Do not send requester metadata or query history to Agent.
		if task != nil {
			task.Actor = ""
			task.ActorName = ""
			task.ActorID = 0
		}
		writeJSON(w, 200, map[string]any{"task": task})
	}
}
