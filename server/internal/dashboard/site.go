package dashboard

import "controltower/server/internal/storage"

func siteOf(instance storage.Instance) string {
	if instance.SiteID != "" {
		return instance.SiteID
	}
	return instance.ID
}

func (h Handler) instanceIDsForRequest(instanceID, siteID string) ([]string, error) {
	if instanceID != "" {
		return []string{instanceID}, nil
	}
	if siteID == "" {
		return nil, nil
	}
	if h.instanceStore == nil {
		return []string{}, nil
	}
	instances, err := h.instanceStore.ListInstances()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	for _, instance := range instances {
		if siteOf(instance) == siteID {
			ids = append(ids, instance.ID)
		}
	}
	return ids, nil
}

func instanceIDSet(ids []string) map[string]bool {
	if ids == nil {
		return nil
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}
