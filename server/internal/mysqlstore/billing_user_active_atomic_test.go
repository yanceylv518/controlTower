package mysqlstore

import (
	"os"
	"strings"
	"testing"
)

func TestUserDailyActivationDoesNotClearBillsWithoutReplacement(t *testing.T) {
	body, err := os.ReadFile("billing_jobs.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	if strings.Contains(source, "DELETE FROM billing_user_daily_active WHERE instance_id") {
		t.Fatal("activation must not delete an existing user-day before a replacement exists")
	}
	if !strings.Contains(source, "ON DUPLICATE KEY UPDATE job_id=VALUES(job_id),activated_at=VALUES(activated_at)") {
		t.Fatal("user-day activation must atomically replace the active version")
	}
}
