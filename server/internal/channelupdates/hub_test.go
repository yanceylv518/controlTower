package channelupdates

import "testing"

func TestNotifyIsScopedPerSiteAndEmptyWakesAll(t *testing.T) {
	revA, chanA := Listen("a")
	revB, chanB := Listen("b")
	Notify("a")
	select {
	case <-chanA:
	default:
		t.Fatal("site a listener not woken")
	}
	select {
	case <-chanB:
		t.Fatal("site b woken by site a write")
	default:
	}
	if next, _ := Listen("a"); next == revA {
		t.Fatal("site a revision did not advance")
	}
	if next, _ := Listen("b"); next != revB {
		t.Fatal("site b revision changed")
	}
	Notify("")
	select {
	case <-chanB:
	default:
		t.Fatal("unscoped notify must wake every site")
	}
}
