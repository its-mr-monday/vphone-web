package vm

import "testing"

func TestVariantValues(t *testing.T) {
	for _, v := range []Variant{VariantStandard, VariantExperimental, VariantMinimal, VariantRegular, VariantDev, VariantJB, VariantEXP} {
		if !validVariants[v] {
			t.Errorf("%q should be valid", v)
		}
	}
	if validVariants["bogus"] {
		t.Error("'bogus' should not be a valid variant")
	}
}

func TestNormalizeVariant(t *testing.T) {
	cases := []struct{ in, want Variant }{
		{VariantRegular, VariantStandard},
		{VariantDev, VariantStandard},
		{VariantJB, VariantStandard},
		{VariantEXP, VariantExperimental},
		{VariantStandard, VariantStandard},
		{VariantExperimental, VariantExperimental},
		{VariantMinimal, VariantMinimal},
	}
	for _, tc := range cases {
		got := NormalizeVariant(tc.in)
		if got != tc.want {
			t.Errorf("NormalizeVariant(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsUpstreamVM(t *testing.T) {
	legacy := VM{APIToken: ""}
	if legacy.IsUpstreamVM() {
		t.Error("legacy VM without APIToken should not be upstream")
	}
	upstream := VM{APIToken: "abc123"}
	if !upstream.IsUpstreamVM() {
		t.Error("VM with APIToken should be upstream")
	}
}
