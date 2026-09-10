package logarchive

import (
	"controltower/agent/internal/fileatomic"
	ac "controltower/internal/archivecontrol"
	"encoding/json"
)

// AcknowledgeDays only removes the exact snapshot CT accepted, preserving newer
// commits while a control request was in flight. Called by the worker goroutine.
func (w *Worker) AcknowledgeDays(accepted []ac.Day) error {
	if len(accepted) == 0 {
		return nil
	}
	c, err := w.load()
	if err != nil {
		return err
	}
	keep := c.Days[:0]
	for _, d := range c.Days {
		found := false
		for _, a := range accepted {
			if a.Date == d.Date && a.VerifiedAt.Equal(d.VerifiedAt) {
				found = true
				break
			}
		}
		if !found {
			keep = append(keep, d)
		}
	}
	c.Days = keep
	b, _ := json.Marshal(c)
	return fileatomic.WriteFile(w.path, b, 0600)
}
