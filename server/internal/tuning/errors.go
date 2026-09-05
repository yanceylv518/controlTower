package tuning

import "errors"

// ErrDirectControlNotConfigured marks a site that manages channels through
// Agent commands only. Callers that merely want the freshest snapshot treat it
// as "nothing to refresh", not as a failure.
var ErrDirectControlNotConfigured = errors.New("direct control not configured")
