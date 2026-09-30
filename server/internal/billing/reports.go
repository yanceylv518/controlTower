package billing

import (
	"context"
	"errors"
	"time"
)

var ErrReportBusy = errors.New("report generation busy")
var ErrReportCancelled = errors.New("report generation cancelled")

type ReportRow struct {
	UserID       int64  `json:"user_id"`
	User         string `json:"user"`
	Model        string `json:"model"`
	ChannelID    int64  `json:"channel_id"`
	Channel      string `json:"channel"`
	Upstream     string `json:"upstream"`
	Discount     string `json:"discount"`
	CostDiscount string `json:"cost_discount"`
	Requests     int64  `json:"requests"`
	Input        int64  `json:"input"`
	Output       int64  `json:"output"`
	Cache        int64  `json:"cache"`
	Empty        int64  `json:"empty"`
	Amount       string `json:"amount"`
	Cost         string `json:"cost"`
	RawAmount    string `json:"raw_amount"`
	RawCost      string `json:"raw_cost"`
	UnknownCost  int64  `json:"unknown_cost"`
	BaseFallback int64  `json:"base_fallback"`
}
type ReportDocument struct {
	Items          []ReportRow         `json:"items"`
	FailedRequests *int64              `json:"failed_requests"`
	DataSource     string              `json:"data_source"`
	Currency       CurrencyDisplay     `json:"currency"`
	MoneySnapshot  *MoneySnapshot      `json:"money_snapshot"`
	From           time.Time           `json:"from"`
	To             time.Time           `json:"to"`
	GeneratedAt    time.Time           `json:"generated_at"`
	Discounts      []StatementDiscount `json:"discount_snapshot"`
	Upstreams      []Upstream          `json:"upstream_snapshot"`
}
type ReportTaskDay struct {
	Day       string    `json:"day"`
	Status    string    `json:"status"`
	Processed int64     `json:"processed"`
	Error     string    `json:"error"`
	UpdatedAt time.Time `json:"updated_at"`
}
type ReportTask struct {
	ID        string          `json:"id"`
	Site      string          `json:"instance_id"`
	From      string          `json:"from"`
	To        string          `json:"to"`
	Status    string          `json:"status"`
	Overwrite bool            `json:"overwrite"`
	Automatic bool            `json:"automatic"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	Days      []ReportTaskDay `json:"days"`
}
type ReportStore interface {
	CreateReportTask(context.Context, ReportTask) (ReportTask, error)
	ListReportTasks(context.Context, string, int) ([]ReportTask, error)
	CancelReportTask(context.Context, string, string) error
	ReadReportDays(context.Context, string, string, string) ([]ReportDocument, error)
	ReportDayExists(context.Context, string, string) (bool, error)
	LockReportWorker(context.Context) (context.Context, func(), error)
	RecoverReportTasks(context.Context) error
	NextReportTaskDay(context.Context) (ReportTask, ReportTaskDay, error)
	ReportTaskProgress(context.Context, string, string, int64) error
	FinishReportTaskDay(context.Context, ReportTask, ReportTaskDay, *ReportDocument, string) error
	ListBillingSites(context.Context) ([]string, error)
}
