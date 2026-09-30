package billing

import (
	"fmt"
	"math/big"
)

// SettlementDisplay uses the bill's immutable currency observation. Stored
// settlement amounts remain USD; convert only at the presentation boundary.
func SettlementDisplay(snapshot *MoneySnapshot) (CurrencyDisplay, *big.Rat, error) {
	currency := CurrencyDisplay{Type: "USD", Symbol: "$", ExchangeRate: "1"}
	if snapshot != nil {
		currency = snapshot.Display
	}
	rate, ok := new(big.Rat).SetString(currency.ExchangeRate)
	if !ok || rate.Sign() <= 0 {
		return currency, nil, fmt.Errorf("invalid billing display exchange rate")
	}
	if currency.Type == "TOKENS" {
		rate, ok = new(big.Rat).SetString(snapshot.QuotaPerUnit)
		if !ok || rate.Sign() <= 0 {
			return currency, nil, fmt.Errorf("invalid billing quota unit")
		}
	}
	return currency, rate, nil
}
func DisplaySettlementAmount(amount string, rate *big.Rat) string {
	n, ok := new(big.Rat).SetString(amount)
	if !ok {
		return amount
	}
	return new(big.Rat).Mul(n, rate).FloatString(12)
}

func SettlementCurrencyLabel(currency CurrencyDisplay) string {
	if currency.Type == "CUSTOM" {
		return currency.Symbol
	}
	if currency.Type == "TOKENS" {
		return "额度"
	}
	return currency.Type
}
