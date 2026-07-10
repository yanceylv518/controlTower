package dashboard

import "controltower/server/internal/storage"

type LogFilter = storage.LogQuery

func FilterLogs(events []storage.LogEvent, filter LogFilter) []storage.LogEvent {
	return storage.FilterLogEvents(events, filter)
}
