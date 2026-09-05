package directcontrol

import (
	"context"
	"fmt"
	"time"

	"controltower/internal/channelcontrol"
)

func (s Store) RefreshChannels(ctx context.Context, siteID, actor string) error {
	controller, direct, err := s.controllerForSite(siteID)
	if err != nil {
		return err
	}
	if !direct {
		return fmt.Errorf("请先完成站点的 new-api 直连配置，再刷新渠道信息")
	}
	lister, ok := controller.(interface {
		List(context.Context) ([]channelcontrol.Channel, error)
	})
	if !ok {
		return fmt.Errorf("channel listing is not supported by controller")
	}
	at := time.Now().UTC()
	channels, err := lister.List(ctx)
	if err != nil {
		return err
	}
	return s.Store.StoreFreshChannels(siteID, channels, at)
}
