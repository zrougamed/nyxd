package apparmor

import "testing"

func TestAvailableMatchesGOOS(t *testing.T) {
	// On this CI host Available may be true or false; just ensure it does not panic
	// and ProfileLoaded("") is always false.
	_ = Available()
	if ProfileLoaded("") || ProfileLoaded("   ") {
		t.Fatal("empty profile must not be loaded")
	}
	if ProfileLoaded("nyxd-definitely-missing-profile-name-xyz") {
		t.Fatal("missing profile reported loaded")
	}
}
