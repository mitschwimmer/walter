package main

import "testing"

func TestShortRevision(t *testing.T) {
	for _, tc := range []struct{ ref, want string }{
		{"10d95b1d8af184baff1dc426959318280794f88d", "10d95b1"},
		{"10d95b1", "10d95b1"},
		{"10d95b1d8af184baff1dc426959318280794f88d-dirty", "10d95b1-dirty"},
		{"", "dev"},
		{"unknown", "dev"},
		{"dev", "dev"},
	} {
		if got := shortRevision(tc.ref); got != tc.want {
			t.Errorf("shortRevision(%q) = %q, want %q", tc.ref, got, tc.want)
		}
	}
}
