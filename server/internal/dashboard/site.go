package dashboard

import "controltower/server/internal/storage"

func siteOf(instance storage.Instance) string {
	if instance.SiteID != "" {
		return instance.SiteID
	}
	return instance.ID
}
