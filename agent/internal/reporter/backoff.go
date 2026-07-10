package reporter

import "time"

func BackoffDelay(failures int) time.Duration {
	switch {
	case failures <= 0:
		return 0
	case failures == 1:
		return 10 * time.Second
	case failures == 2:
		return 30 * time.Second
	case failures == 3:
		return 60 * time.Second
	case failures == 4:
		return 120 * time.Second
	default:
		return 300 * time.Second
	}
}

