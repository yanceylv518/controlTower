package billing

import "context"

// A separate on-demand diagnostic period. It never participates in daily/monthly settlement.
const ZeroOutputPeriod = "zero_output"

type zeroOutputContextKey struct{}

func WithZeroOutputOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, zeroOutputContextKey{}, true)
}

func ZeroOutputOnly(ctx context.Context) bool {
	v, _ := ctx.Value(zeroOutputContextKey{}).(bool)
	return v
}
