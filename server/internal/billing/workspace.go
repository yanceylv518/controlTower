package billing

type WorkspaceBill struct {
	WorkspaceTotals
	Currency CurrencyDisplay  `json:"currency"`
	Job      Job              `json:"job"`
	Tiers    []TierStatistics `json:"tiers,omitempty"`
	Models   []WorkspaceModel `json:"models,omitempty"`
}

type WorkspaceModel struct {
	WorkspaceTotals
	Model string `json:"model"`
}

type WorkspaceTotals struct {
	MultimediaUsage
	Discount     string `json:"discount"`
	Requests     int64  `json:"requests"`
	Input        int64  `json:"input"`
	Output       int64  `json:"output"`
	CacheRead    int64  `json:"cache_read"`
	CacheWrite   int64  `json:"cache_write"`
	Amount       string `json:"amount"`
	BeforeAmount string `json:"before_amount"`
	EmptyCount   int64  `json:"empty_count"`
	EmptyAmount  string `json:"empty_amount"`
}

// Sum the saved model aggregates exactly, without repricing or reading source logs.
func SumWorkspaceModels(models []WorkspaceModel) WorkspaceTotals {
	total := WorkspaceTotals{Amount: "0.000000000000", EmptyAmount: "0.000000000000"}
	for i, model := range models {
		total.Requests += model.Requests
		total.Input += model.Input
		total.Output += model.Output
		total.CacheRead += model.CacheRead
		total.CacheWrite += model.CacheWrite
		total.ImageInputTokens += model.ImageInputTokens
		total.ImageOutputTokens += model.ImageOutputTokens
		total.AudioInputTokens += model.AudioInputTokens
		total.AudioOutputTokens += model.AudioOutputTokens
		total.EmptyCount += model.EmptyCount
		total.Amount = MergeBefore(total.Amount, model.Amount)
		total.EmptyAmount = MergeBefore(total.EmptyAmount, model.EmptyAmount)
		if i == 0 {
			total.BeforeAmount, total.Discount = model.BeforeAmount, model.Discount
		} else {
			total.BeforeAmount = MergeBefore(total.BeforeAmount, model.BeforeAmount)
			total.Discount = MergeDiscount(total.Discount, model.Discount)
		}
	}
	return total
}
