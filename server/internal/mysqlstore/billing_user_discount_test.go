package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"controltower/server/internal/billing"
)

func TestBillingUserDiscountRangesAndIsolation(t *testing.T) {
	dsn := os.Getenv("CT_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("requires local CT_MYSQL_TEST_DSN")
	}
	db, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err = ApplyDir(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	site := fmt.Sprintf("user-discount-%d", time.Now().UnixNano())
	otherSite := site + "-other"
	defer func() {
		if _, cleanupErr := db.Exec("DELETE FROM billing_discount_rules WHERE instance_id IN (?,?)", site, otherSite); cleanupErr != nil {
			t.Error(cleanupErr)
		}
	}()
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, billing.BusinessLocation)
	boundary := from.AddDate(0, 0, 1)
	input := billing.DiscountRule{InstanceID: site, DiscountType: billing.DiscountUserModel,
		SubjectID: 7, ModelName: "model-a", Discount: "0.7", EffectiveFrom: from, UpdatedBy: "test"}
	first, err := s.PutBillingDiscountRule(ctx, input)
	if err != nil || first.ID <= 0 {
		t.Fatalf("open-ended insert: %+v %v", first, err)
	}
	readFirst := func() billing.DiscountRule {
		t.Helper()
		items, readErr := s.ListBillingDiscountRules(ctx, site, billing.DiscountUserModel)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, item := range items {
			if item.ID == first.ID {
				return item
			}
		}
		t.Fatal("saved rule missing")
		return billing.DiscountRule{}
	}
	saved := readFirst()
	if saved.EffectiveTo != nil || !saved.EffectiveFrom.Equal(from) || saved.Discount != "0.700000" {
		t.Fatalf("persisted open-ended rule: %+v", saved)
	}
	if _, err = s.PutBillingDiscountRule(ctx, input); !errors.Is(err, billing.ErrDiscountOverlap) {
		t.Fatalf("overlap accepted: %v", err)
	}
	for _, isolated := range []billing.DiscountRule{
		{InstanceID: site, SubjectID: 8, ModelName: "model-a"},
		{InstanceID: otherSite, SubjectID: 7, ModelName: "model-a"},
		{InstanceID: site, SubjectID: 7, ModelName: "Model-A"},
	} {
		isolated.DiscountType, isolated.Discount, isolated.EffectiveFrom = billing.DiscountUserModel, "0.8", from
		if _, err = s.PutBillingDiscountRule(ctx, isolated); err != nil {
			t.Fatalf("independent rule rejected: %+v %v", isolated, err)
		}
	}
	first.EffectiveTo = &boundary
	if first, err = s.PutBillingDiscountRule(ctx, first); err != nil {
		t.Fatal(err)
	}
	input.EffectiveFrom = boundary
	second, err := s.PutBillingDiscountRule(ctx, input)
	if err != nil {
		t.Fatalf("adjacent interval rejected: %v", err)
	}
	first.EffectiveTo = nil
	if _, err = s.PutBillingDiscountRule(ctx, first); !errors.Is(err, billing.ErrDiscountOverlap) {
		t.Fatalf("extension overlap accepted: %v", err)
	}
	if saved = readFirst(); saved.EffectiveTo == nil || !saved.EffectiveTo.Equal(boundary) {
		t.Fatalf("failed update changed persisted period: %+v", saved)
	}
	if err = s.DeleteBillingDiscountRule(ctx, otherSite, second.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-site deletion accepted: %v", err)
	}
	if err = s.DeleteBillingDiscountRule(ctx, site, second.ID); err != nil {
		t.Fatal(err)
	}
	if first, err = s.PutBillingDiscountRule(ctx, first); err != nil {
		t.Fatalf("clearing end after removing overlap: %v", err)
	}
	if saved = readFirst(); saved.EffectiveTo != nil {
		t.Fatalf("end was not cleared: %+v", saved)
	}
	first.InstanceID = site + "-missing"
	if _, err = s.PutBillingDiscountRule(ctx, first); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-site update accepted: %v", err)
	}
	input.ModelName = "concurrent-model"
	var wg sync.WaitGroup
	results := make([]error, 2)
	for index := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = s.PutBillingDiscountRule(ctx, input)
		}(index)
	}
	wg.Wait()
	success, overlap := 0, 0
	for _, writeErr := range results {
		if writeErr == nil {
			success++
		} else if errors.Is(writeErr, billing.ErrDiscountOverlap) {
			overlap++
		} else {
			t.Fatal(writeErr)
		}
	}
	if success != 1 || overlap != 1 {
		t.Fatalf("concurrent overlapping writes: success=%d overlap=%d", success, overlap)
	}
}
