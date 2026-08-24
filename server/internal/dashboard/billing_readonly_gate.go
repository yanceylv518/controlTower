package dashboard

import "strings"

type billingReadonlyConfig interface {
	ReadonlyDSNForSite(string) (string, error)
}

func billingReadonlyAvailable(store any, site string) bool {
	config, ok := store.(billingReadonlyConfig)
	if !ok {
		// Test and alternate stores without instance configuration keep their
		// existing behavior; the production MySQL store always implements this.
		return true
	}
	value, err := config.ReadonlyDSNForSite(site)
	return err == nil && strings.TrimSpace(value) != ""
}
