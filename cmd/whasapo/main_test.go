package main

import (
	"testing"
)

func TestSemverGreater(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"1.0.0", "0.9.0", true},
		{"0.2.0", "0.1.0", true},
		{"0.1.1", "0.1.0", true},
		{"1.0.0", "1.0.0", false},
		{"0.1.0", "0.2.0", false},
		{"0.0.1", "0.0.2", false},
		// Multi-digit segments (the bug this fixed)
		{"0.10.0", "0.2.0", true},
		{"0.2.0", "0.10.0", false},
		{"1.0.0", "0.99.0", true},
		{"2.0.0", "1.99.99", true},
		// Different lengths
		{"1.0", "0.9.9", true},
		{"1.0.0.1", "1.0.0", true},
		{"1.0", "1.0.0", false},
		// Zero
		{"0.0.0", "0.0.0", false},
		{"0.0.1", "0.0.0", true},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			got := semverGreater(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("semverGreater(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
