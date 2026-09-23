package archivecontract

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRawPositionContract(t *testing.T) {
	now := time.Now().UTC()
	p := RawPosition{Table: "logs_202609", ID: 9007199254740993, LogTime: &now, ObservedAt: now}
	if !p.Valid() {
		t.Fatal("valid position rejected")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["id"] != "9007199254740993" {
		t.Fatal("ID must retain precision", string(raw))
	}
	if !(RawPosition{ObservedAt: now}).Valid() {
		t.Fatal("empty inventory rejected")
	}
	for _, bad := range []RawPosition{
		{Table: "logs_202613", ID: 1, LogTime: &now, ObservedAt: now},
		{Table: "logs_202609", ID: 1, ObservedAt: now},
		{Table: "logs", ID: 1, LogTime: &now, ObservedAt: now},
		{ID: 1, ObservedAt: now},
		{},
	} {
		if bad.Valid() {
			t.Fatalf("accepted invalid position: %+v", bad)
		}
	}
}
