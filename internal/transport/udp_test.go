package transport

import "testing"

func TestLinuxInterfaceNames(t *testing.T) {
	tests := []struct {
		srlName    string
		wantParent string
		wantSub    string
	}{
		{"ethernet-1/1", "e1-1", "e1-1.0"},
		{"ethernet-1/2", "e1-2", "e1-2.0"},
		{"ethernet-1/3", "e1-3", "e1-3.0"},
		{"ethernet-1/10", "e1-10", "e1-10.0"},
	}

	for _, tt := range tests {
		t.Run(tt.srlName, func(t *testing.T) {
			parent, sub := LinuxInterfaceNames(tt.srlName)
			if parent != tt.wantParent {
				t.Errorf("parent: got %q, want %q", parent, tt.wantParent)
			}
			if sub != tt.wantSub {
				t.Errorf("sub: got %q, want %q", sub, tt.wantSub)
			}
		})
	}
}
