package channelcontrol

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeGroupCanonicalizesAndClears(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{name: "combination", input: " default, vip,default ", want: "default,vip"},
		{name: "clear", input: "  ", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeGroup(test.input)
			if err != nil || got != test.want {
				t.Fatalf("NormalizeGroup(%q) = %q, %v; want %q", test.input, got, err, test.want)
			}
		})
	}
}

func TestNormalizeGroupRejectsInvalidItems(t *testing.T) {
	for _, input := range []string{"default,,vip", "default,", "\n", "x" + string(rune(0x7f))} {
		if _, err := NormalizeGroup(input); err == nil {
			t.Fatalf("NormalizeGroup(%q) accepted invalid group", input)
		}
	}
	if _, err := NormalizeGroup(strings.Repeat("x", MaxGroupLength+1)); err == nil {
		t.Fatal("NormalizeGroup accepted an overlong group")
	}
}

func TestValidateKnownGroupsRejectsUnlistedNames(t *testing.T) {
	if err := ValidateKnownGroups("default,vip", []string{"default", "vip,fast"}); err != nil {
		t.Fatalf("known groups rejected: %v", err)
	}
	if err := ValidateKnownGroups("custom", []string{"default", "vip,fast"}); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("unknown group error = %v, want ErrGroupNotFound", err)
	}
	if err := ValidateKnownGroups("", nil); err != nil {
		t.Fatalf("clearing groups rejected: %v", err)
	}
}
