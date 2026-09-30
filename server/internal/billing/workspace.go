package billing

type WorkspaceBill struct {
	MultimediaUsage
	Discount     string          `json:"discount"`
	Currency     CurrencyDisplay `json:"currency"`
	Job          Job             `json:"job"`
	Requests     int64           `json:"requests"`
	Input        int64           `json:"input"`
	Output       int64           `json:"output"`
	CacheRead    int64           `json:"cache_read"`
	CacheWrite   int64           `json:"cache_write"`
	Amount       string          `json:"amount"`
	BeforeAmount string          `json:"before_amount"`
	EmptyCount   int64           `json:"empty_count"`
	EmptyAmount  string          `json:"empty_amount"`
}
