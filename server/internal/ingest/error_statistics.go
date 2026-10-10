package ingest

import (
	"context"
	es "controltower/internal/errorstats"
	"fmt"
)

func (s Service) SaveErrorStatistics(ctx context.Context, b es.Batch) error {
	sink, ok := s.store.(es.Sink)
	if !ok {
		return fmt.Errorf("error statistics unavailable")
	}
	return sink.SaveErrorStatistics(ctx, b)
}
