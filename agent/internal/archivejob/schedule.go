package archivejob

import aj "controltower/internal/archivejob"

// Counters are persisted with the batch. A ratio change starts a fresh cycle,
// while old checkpoints retain their existing collection turns.
func (s *state) nextHistoryTurn(settings aj.Settings) bool {
	c, h := settings.BatchRatio()
	if s.ScheduleCollection != 0 && (s.ScheduleCollection != c || s.ScheduleHistory != h) {
		s.Turns, s.HistoryTurns = 0, 0
	}
	s.ScheduleCollection, s.ScheduleHistory = c, h
	if !settings.Collection || !settings.History {
		s.Turns, s.HistoryTurns = 0, 0
		return settings.History
	}
	if s.Turns < c {
		s.Turns++
		return false
	}
	s.HistoryTurns++
	if s.HistoryTurns >= h {
		s.Turns, s.HistoryTurns = 0, 0
	}
	return true
}
