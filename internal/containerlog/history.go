package containerlog

import "time"

type HistoryFilter struct {
	ActorID                int64
	Site, Actor, RequestID string
	From, To               time.Time
	Page, PageSize         int
}

type HistoryGroup struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Tasks     []Task    `json:"tasks"`
}

type HistoryPage struct {
	Items          []HistoryGroup `json:"items"`
	Total          int            `json:"total"`
	Page           int            `json:"page"`
	PageSize       int            `json:"page_size"`
	CanFilterActor bool           `json:"can_filter_actor"`
}
