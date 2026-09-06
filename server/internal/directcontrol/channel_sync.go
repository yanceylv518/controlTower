package directcontrol

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controltower/internal/channelcontrol"
	"controltower/server/internal/tuning"
)

func (s Store) RefreshChannels(ctx context.Context, siteID, actor string) error {
	controller, direct, err := s.controllerForSite(siteID)
	if err != nil {
		return err
	}
	if !direct {
		return tuning.ErrDirectControlNotConfigured
	}
	lister, ok := controller.(interface {
		List(context.Context) ([]channelcontrol.Channel, error)
	})
	if !ok {
		return fmt.Errorf("channel listing is not supported by controller")
	}
	// A list that changed under pagination is retried a few times before the
	// operator sees a failure; the snapshot time is taken before each attempt
	// so a write landing during the read still wins over the stored rows.
	var channels []channelcontrol.Channel
	var at time.Time
	for attempt := 1; ; attempt++ {
		at = time.Now().UTC()
		var err error
		channels, err = lister.List(ctx)
		if err == nil {
			break
		}
		if !errors.Is(err, channelcontrol.ErrListChanged) || attempt >= channelListAttempts {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(channelListRetryDelay):
		}
	}
	return s.Store.StoreFreshChannels(siteID, channels, at)
}

const (
	channelListAttempts   = 3
	channelListRetryDelay = 500 * time.Millisecond
)
