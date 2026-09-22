package archivepipeline

import "testing"

func TestCheckpointCannotResetMigrationOrLoseReservations(t *testing.T) {
	s := fixture(t)
	c := reserve(t, s, Collection)
	finish(t, s, reserve(t, s, Migration), "", "")
	observe(t, s, "2026-09-20", "2026-09-21")
	o := reserve(t, s, Organization)
	raw, err := s.Encode()
	if err != nil {
		t.Fatal(err)
	}
	r, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !r.MigrationDone || r.Active[Collection] != c || r.Active[Organization] != o {
		t.Fatal(r)
	}
	for _, raw := range []string{`{}`, `{"version":2}`, `{"version":1,"days":null,"active":{}}`, `{"version":1,"days":{},"active":{},"migration_done":true}`} {
		if _, err := Decode([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid checkpoint %s", raw)
		}
	}
}

func TestCheckpointRejectsImpossibleParallelism(t *testing.T) {
	s := fixture(t)
	s.MigrationDone = true
	observe(t, s, "2026-09-20", "2026-09-21")
	finish(t, s, reserve(t, s, Organization), "", "")
	v := reserve(t, s, Verification)
	s.NextToken++
	s.Active[Collection] = Work{Token: s.NextToken, Task: Collection}
	if s.Validate() == nil {
		t.Fatal("accepted two source readers")
	}
	delete(s.Active, Collection)
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := s.ObserveCommittedRows(nil, map[string]bool{v.Date: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}
