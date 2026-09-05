// Package channelupdates wakes dashboard long polls after persisted channel changes.
package channelupdates

import (
	"strconv"
	"sync"
	"time"
)

type siteState struct {
	revision uint64
	changed  chan struct{}
}

var (
	mu    sync.Mutex
	sites = map[string]*siteState{}
	epoch = strconv.FormatInt(time.Now().UnixNano(), 36)
)

func stateFor(siteID string) *siteState {
	state := sites[siteID]
	if state == nil {
		state = &siteState{changed: make(chan struct{})}
		sites[siteID] = state
	}
	return state
}

// Listen returns the site's revision and its notification atomically, avoiding
// a missed write between the initial read and beginning to wait. Revisions are
// scoped per site so one site's writes do not wake every other site's page.
func Listen(siteID string) (string, <-chan struct{}) {
	mu.Lock()
	defer mu.Unlock()
	state := stateFor(siteID)
	return epoch + ":" + strconv.FormatUint(state.revision, 10), state.changed
}

// Notify wakes listeners of one site; an empty site wakes every site (used
// when the writer cannot resolve the site).
func Notify(siteID string) {
	mu.Lock()
	defer mu.Unlock()
	if siteID != "" {
		bump(stateFor(siteID))
		return
	}
	for _, state := range sites {
		bump(state)
	}
}

func bump(state *siteState) {
	state.revision++
	close(state.changed)
	state.changed = make(chan struct{})
}
