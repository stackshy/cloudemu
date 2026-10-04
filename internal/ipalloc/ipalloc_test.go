package ipalloc

import "testing"

func TestFirstFree(t *testing.T) {
	tests := []struct {
		name string
		cidr string
		used map[string]bool
		want string
	}{
		{"skips reserved low addresses", "10.1.0.0/24", nil, "10.1.0.4"},
		{"skips used", "10.1.0.0/24", map[string]bool{"10.1.0.4": true}, "10.1.0.5"},
		{"exhausted", "10.1.0.0/29", map[string]bool{"10.1.0.4": true, "10.1.0.5": true, "10.1.0.6": true}, ""},
		{"too small", "10.1.0.0/30", nil, ""},
		{"not ipv4", "fd00::/64", nil, ""},
		{"malformed", "nope", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FirstFree(tt.cidr, tt.used); got != tt.want {
				t.Errorf("FirstFree(%q)=%q want %q", tt.cidr, got, tt.want)
			}
		})
	}
}

func TestUsable(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"10.1.0.4", true},
		{"10.1.255.254", true},
		{"10.1.0.1", false},
		{"10.1.255.255", false},
		{"10.99.0.5", false},
		{"bogus", false},
	}

	for _, tt := range tests {
		if got := Usable("10.1.0.0/16", tt.ip); got != tt.want {
			t.Errorf("Usable(%q)=%v want %v", tt.ip, got, tt.want)
		}
	}
}
