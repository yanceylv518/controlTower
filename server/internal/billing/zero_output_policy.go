package billing

// MonthlyZeroOutputPolicy describes the immutable daily sources of a month.
func MonthlyZeroOutputPolicy(total, known, excluded int64) string {
	if total == 0 || known != total {
		return "unknown"
	}
	if excluded == 0 {
		return "included"
	}
	if excluded == total {
		return "excluded"
	}
	return "mixed"
}
