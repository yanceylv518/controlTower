package tuning

import "errors"

// ErrDirectControlNotConfigured marks a site that manages channels through
// Agent commands only. Callers that merely want the freshest snapshot treat it
// as "nothing to refresh", not as a failure.
var ErrDirectControlNotConfigured = errors.New("direct control not configured")

// ErrChannelNotFound 表示目标渠道不属于当前站点，避免跨站点下发配置命令。
var ErrChannelNotFound = errors.New("channel not found in site")
