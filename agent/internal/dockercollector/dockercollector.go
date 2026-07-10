package dockercollector

import (
	"bufio"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"controltower/agent/internal/reporter"
)

type Collector struct {
	dockerPath string
	now        func() time.Time
}

func New() Collector {
	return Collector{dockerPath: "docker", now: time.Now}
}

func NewWithPath(dockerPath string) Collector {
	if dockerPath == "" {
		dockerPath = "docker"
	}
	return Collector{dockerPath: dockerPath, now: time.Now}
}

func (c Collector) Collect(ctx context.Context) []reporter.DockerStatusPayload {
	collectedAt := c.now().UTC()
	output, err := exec.CommandContext(ctx, c.dockerPath, "ps", "--all", "--format", "{{.Names}}\t{{.Status}}\t{{.State}}").Output()
	if err != nil {
		return nil
	}
	return ParseStatuses(string(output), collectedAt)
}

func ParseStatuses(output string, collectedAt time.Time) []reporter.DockerStatusPayload {
	var statuses []reporter.DockerStatusPayload
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		payload, err := parseLine(scanner.Text(), collectedAt)
		if err == nil {
			statuses = append(statuses, payload)
		}
	}
	return statuses
}

func parseLine(line string, collectedAt time.Time) (reporter.DockerStatusPayload, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return reporter.DockerStatusPayload{}, errors.New("empty docker status line")
	}
	parts := strings.Split(line, "\t")
	if len(parts) < 2 {
		return reporter.DockerStatusPayload{}, errors.New("invalid docker status line")
	}
	name := strings.TrimSpace(parts[0])
	status := strings.TrimSpace(parts[1])
	state := ""
	if len(parts) > 2 {
		state = strings.TrimSpace(parts[2])
	}
	if name == "" {
		return reporter.DockerStatusPayload{}, errors.New("missing container name")
	}
	return reporter.DockerStatusPayload{
		CollectedAt:   collectedAt,
		ContainerName: name,
		Status:        sanitizeStatus(status),
		Running:       isRunningStatus(status, state),
	}, nil
}

func sanitizeStatus(status string) string {
	status = strings.ReplaceAll(status, "\r", " ")
	status = strings.ReplaceAll(status, "\n", " ")
	status = strings.TrimSpace(status)
	if len(status) > 200 {
		status = status[:200]
	}
	return status
}

func isRunningStatus(status string, state string) bool {
	state = strings.ToLower(strings.TrimSpace(state))
	if state == "running" {
		return true
	}
	status = strings.ToLower(strings.TrimSpace(status))
	return strings.HasPrefix(status, "up ") || status == "up"
}
