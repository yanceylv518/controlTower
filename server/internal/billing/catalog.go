package billing

import "time"

// CatalogFilter scopes discovery to issued settlement bills, never source logs.
type CatalogFilter struct {
	Site, Kind, Period, Month, Query string
	Page, PageSize                   int
}

type CatalogBill struct {
	WorkspaceBill
	GeneratedAt time.Time `json:"generated_at"`
}

type CatalogPage struct {
	Items    []CatalogBill    `json:"items"`
	Total    int64            `json:"total"`
	Subjects int64            `json:"subjects"`
	Counts   map[string]int64 `json:"counts"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}
