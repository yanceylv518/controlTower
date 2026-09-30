package billing

import "testing"

func TestSumWorkspaceModelsPreservesAmountsAndUnknownOriginals(t *testing.T) {
	models := []WorkspaceModel{
		{Model: "a", WorkspaceTotals: WorkspaceTotals{Requests: 2, Input: 17, Output: 8, CacheRead: 6, CacheWrite: 3, MultimediaUsage: MultimediaUsage{ImageInputTokens: 4, ImageOutputTokens: 5, AudioInputTokens: 6, AudioOutputTokens: 7}, Amount: "0.012280000001", BeforeAmount: "0.026696", Discount: "0.460000", EmptyCount: 1, EmptyAmount: "0.000001"}},
		{Model: "b", WorkspaceTotals: WorkspaceTotals{Requests: 1, Input: 1, Output: 2, Amount: "0.01", BeforeAmount: "0.01", Discount: "1", EmptyAmount: "0"}},
	}
	total := SumWorkspaceModels(models)
	if total.Requests != 3 || total.Input != 18 || total.Output != 10 || total.CacheRead != 6 || total.CacheWrite != 3 || total.ImageInputTokens != 4 || total.ImageOutputTokens != 5 || total.AudioInputTokens != 6 || total.AudioOutputTokens != 7 || total.EmptyCount != 1 {
		t.Fatalf("usage not preserved: %+v", total)
	}
	if total.Amount != "0.022280000001" || total.BeforeAmount != "0.036696000000" || total.Discount != "mixed" || total.EmptyAmount != "0.000001000000" {
		t.Fatalf("amounts not preserved: %+v", total)
	}
	models[1].BeforeAmount = ""
	if got := SumWorkspaceModels(models); got.BeforeAmount != "" || got.Amount != total.Amount {
		t.Fatalf("unknown original must remain unknown: %+v", got)
	}
	models[1].Discount = "0.46"
	if got := SumWorkspaceModels(models); got.Discount != "0.460000" {
		t.Fatal(got.Discount)
	}
	if got := SumWorkspaceModels(nil); got.Requests != 0 || got.Amount != "0.000000000000" || got.BeforeAmount != "" {
		t.Fatal(got)
	}
}
