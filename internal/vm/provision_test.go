package vm

import "testing"

func TestValidVariants(t *testing.T) {
	for _, v := range []Variant{VariantRegular, VariantDev, VariantJB, VariantEXP} {
		if !validVariants[v] {
			t.Errorf("expected %q to be a valid variant", v)
		}
	}
	if validVariants["bogus"] {
		t.Error("expected 'bogus' to be invalid")
	}
}

func TestDiskGiB(t *testing.T) {
	cases := []struct{ mib, want int }{
		{16384, 16}, {65536, 64}, {512, 1}, {0, 1}, {1024, 1},
	}
	for _, c := range cases {
		if got := diskGiB(c.mib); got != c.want {
			t.Errorf("diskGiB(%d) = %d, want %d", c.mib, got, c.want)
		}
	}
}

func TestIsDFUReady(t *testing.T) {
	ready := []string{
		"[vphone] VM started in DFU mode - connect with irecovery",
		"Entering recovery mode",
		"device is now in DFU mode",
	}
	for _, l := range ready {
		if !isDFUReady(l) {
			t.Errorf("expected DFU-ready for %q", l)
		}
	}
	if isDFUReady("booting normally") {
		t.Error("false positive DFU-ready match")
	}
}

func TestKeyAliasMapping(t *testing.T) {
	s := &Socket{}
	// Unknown key errors; known aliases don't (they fail later at dial, not here).
	if err := s.Key("nonsense"); err == nil {
		t.Error("expected error for unknown key")
	}
	for _, k := range []string{"home", "lock", "volume_up", "volume_down"} {
		if _, ok := keyAlias[k]; !ok {
			t.Errorf("expected alias for %q", k)
		}
	}
	if keyAlias["lock"] != "power" {
		t.Errorf("lock should map to power, got %q", keyAlias["lock"])
	}
}
