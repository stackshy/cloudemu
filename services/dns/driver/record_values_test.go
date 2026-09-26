package driver

import (
	"errors"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

func TestValidateAddresses(t *testing.T) {
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
			err := ValidateAddresses(tt.rtype, tt.values)
			if !tt.wantBad {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				return
			}

			var ae *InvalidAddressError
			if !errors.As(err, &ae) || ae.Index != tt.wantIdx || ae.Value != tt.values[tt.wantIdx] {
				t.Fatalf("got %v, want InvalidAddressError at index %d", err, tt.wantIdx)
			}

			if !cerrors.IsInvalidArgument(err) {
				t.Fatalf("error %v is not InvalidArgument", err)
			}
		})
	}
}

func TestInvalidAddressErrorMessage(t *testing.T) {
	tests := []struct {
		err  *InvalidAddressError
		want string
	}{
		{&InvalidAddressError{RecordType: "A", Value: "x"}, "The value 'x' of field 'ipv4Address' is not a valid IPv4 address."},
		{&InvalidAddressError{RecordType: "AAAA", Value: "x"}, "The value 'x' of field 'ipv6Address' is not a valid IPv6 address."},
		{&InvalidAddressError{RecordType: "A"}, "The resource record is missing field 'ipv4Address'."},
	}

	for _, tt := range tests {
		if got := cerrors.Message(tt.err); got != tt.want {
			t.Errorf("Message = %q, want %q", got, tt.want)
		}
	}
}
