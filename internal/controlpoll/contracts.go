package controlpoll

import "encoding/json"

// Sections are acknowledged separately: one failed operation must not replay
// another operation whose transaction has already committed.
type Request map[string]json.RawMessage
type Result struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}
type Response map[string]Result
