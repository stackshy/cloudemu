package driver

import "testing"

func TestInvalidAddressIndex(t *testing.T) {
	tests := []struct {
		name    string
		rtype   string
		values  []string
		wantIdx int
		wantBad bool
	}{
		{"valid A", "A", []string{"192.0.2.1", "198.51.100.7"}, 0, false},
		{"bad A second", "A", []string{"192.0.2.1", "999.1.1.1"}, 1, true},
		{"A holding IPv6", "A", []string{"2001:db8::1"}, 0, true},
		{"empty A", "A", []string{""}, 0, true},
		{"lower-case type", "a", []string{"not-an-ip"}, 0, true},
		{"valid AAAA", "AAAA", []string{"2001:db8::1"}, 0, false},
		{"AAAA holding IPv4", "AAAA", []string{"192.0.2.1"}, 0, true},
		{"AAAA mapped IPv4", "AAAA", []string{"::ffff:192.0.2.1"}, 0, true},
		{"CNAME ignored", "CNAME", []string{"not-an-ip"}, 0, false},
		{"TXT ignored", "TXT", []string{"hello"}, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, bad := InvalidAddressIndex(tt.rtype, tt.values)
			if bad != tt.wantBad || (bad && idx != tt.wantIdx) {
				t.Fatalf("InvalidAddressIndex(%q, %v) = (%d, %v), want (%d, %v)",
					tt.rtype, tt.values, idx, bad, tt.wantIdx, tt.wantBad)
			}
		})
	}
}
