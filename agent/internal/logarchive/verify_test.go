package logarchive

import (
	"context"
	"strings"
	"testing"
)

func TestVerificationFailureRollsBackAndRetries(t *testing.T) {
	for _, failure := range []string{"missing", "corrupt"} {
		t.Run(failure, func(t *testing.T) {
			w, _, dst := worker(t)
			dst.missingVerification = failure == "missing"
			dst.corruptVerification = failure == "corrupt"
			_, err := w.Pass(context.Background())
			if err == nil || !strings.Contains(err.Error(), "verification") || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe or missing verification error: %v", err)
			}
			cp, err := w.load()
			if err != nil || cp.AfterID != 0 || !cp.LastVerifiedAt.IsZero() || dst.commits != 0 || len(dst.ledger) != 0 || len(dst.stats) != 0 || dst.rollbacks != 1 {
				t.Fatalf("verification failure advanced state: %+v err=%v commits=%d", cp, err, dst.commits)
			}
			dst.missingVerification = false
			dst.corruptVerification = false
			n, err := w.Pass(context.Background())
			if err != nil || n != 2 {
				t.Fatalf("retry: %d %v", n, err)
			}
			cp, err = w.load()
			if err != nil || cp.LastVerifiedRows != 2 || cp.LastVerifiedAt.IsZero() || cp.AfterID != 2 {
				t.Fatalf("verification not recorded: %+v %v", cp, err)
			}
		})
	}
}
