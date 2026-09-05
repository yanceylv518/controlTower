// Package channelupdates wakes dashboard long polls after persisted channel changes.
package channelupdates

import (
	"strconv"
	"sync"
	"time"
)

var mu sync.Mutex
var revision uint64
var changed = make(chan struct{})
var epoch = strconv.FormatInt(time.Now().UnixNano(), 36)

// Listen returns the revision and its notification atomically, avoiding a
// missed write between the initial read and beginning to wait.
func Listen() (string, <-chan struct{}) {
	mu.Lock()
	defer mu.Unlock()
	return epoch + ":" + strconv.FormatUint(revision, 10), changed
}

func Notify() {
	mu.Lock()
	defer mu.Unlock()
	revision++
	close(changed)
	changed = make(chan struct{})
}
