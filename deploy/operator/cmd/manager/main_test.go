package main

import "testing"

func TestValidateRouteMode(t *testing.T) {
	for _, mode := range []string{"", "gateway", "ingress"} {
		if err := validateRouteMode(mode); err != nil {
			t.Fatalf("validateRouteMode(%q) error = %v", mode, err)
		}
	}

	if err := validateRouteMode("mesh"); err == nil {
		t.Fatalf("validateRouteMode(mesh) error = nil, want error")
	}
}
