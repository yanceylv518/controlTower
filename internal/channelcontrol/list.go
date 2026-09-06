package channelcontrol

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// ErrListChanged reports that channels were added or removed while the list
// was being paged. Offset pagination silently shifts under a deletion: a row
// that would have started the next page slides onto the previous page and is
// never seen, so the incomplete list must be discarded and the read retried.
var ErrListChanged = errors.New("new-api channel list changed during pagination; retry refresh")

// Channel contains only public channel metadata, never credentials.
type Channel struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Status   int    `json:"status"`
	Weight   uint   `json:"weight"`
	Priority int64  `json:"priority"`
	Models   string `json:"models"`
	Group    string `json:"group"`
}

// List reads every page before returning. An incomplete list must never be
// used as a full snapshot (which would delete channels on unseen pages).
func (c *Client) List(ctx context.Context) ([]Channel, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	if c.adminUserID <= 0 {
		return nil, fmt.Errorf("new-api admin user id is not configured")
	}
	items := make([]Channel, 0)
	seen := map[int64]bool{}
	expectedTotal := -1
	for page := 1; page <= 10000; page++ {
		var response struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
			Data    struct {
				Items *[]Channel `json:"items"`
				Total *int       `json:"total"`
			} `json:"data"`
		}
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/api/channel/?p=%d&page_size=100&id_sort=true", c.baseURL, page), nil, &response, true); err != nil {
			return nil, err
		}
		if !response.Success {
			return nil, fmt.Errorf("new-api channel list failed: %s", response.Message)
		}
		if response.Data.Items == nil || response.Data.Total == nil {
			return nil, fmt.Errorf("new-api channel list missing items or total")
		}
		// A deletion between pages lowers the reported total while the page
		// offsets keep counting from the original list; the only safe reaction
		// is to start over.
		if expectedTotal >= 0 && *response.Data.Total != expectedTotal {
			return nil, ErrListChanged
		}
		expectedTotal = *response.Data.Total
		for _, item := range *response.Data.Items {
			if item.ID <= 0 || seen[item.ID] {
				return nil, ErrListChanged
			}
			seen[item.ID] = true
			items = append(items, item)
		}
		if len(items) > expectedTotal {
			return nil, ErrListChanged
		}
		if len(items) == expectedTotal {
			return items, nil
		}
		if len(*response.Data.Items) == 0 {
			return nil, fmt.Errorf("new-api channel list is incomplete")
		}
	}
	return nil, fmt.Errorf("new-api channel list exceeds page limit")
}
