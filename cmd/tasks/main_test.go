package main

import "testing"

func TestResolveCollapse(t *testing.T) {
	tests := []struct {
		name    string
		config  bool
		on      bool
		off     bool
		want    bool
		wantErr bool
	}{
		{"default uses config false", false, false, false, false, false},
		{"default uses config true", true, false, false, true, false},
		{"collapse flag enables", false, true, false, true, false},
		{"collapse flag overrides config true", true, true, false, true, false},
		{"no-collapse flag disables", true, false, true, false, false},
		{"no-collapse flag overrides config false", false, false, true, false, false},
		{"both flags error", false, true, true, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveCollapse(tt.config, tt.on, tt.off)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolveCollapse(%v,%v,%v) = %v, want %v", tt.config, tt.on, tt.off, got, tt.want)
			}
		})
	}
}
