package driver

import (
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

func TestValidateSecurityRule(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		from, to int
		wantErr  bool
	}{
		{name: "single port", protocol: "tcp", from: 22, to: 22},
		{name: "full range", protocol: "tcp", from: 0, to: 65535},
		{name: "to above max", protocol: "tcp", from: 0, to: 99999, wantErr: true},
		{name: "negative from", protocol: "udp", from: -1, to: 80, wantErr: true},
		{name: "from greater than to", protocol: "*", from: 443, to: 80, wantErr: true},
		{name: "icmp skips ports", protocol: "icmp", from: -1, to: -1},
		{name: "all skips ports", protocol: "all", from: -1, to: -1},
		{name: "minus one skips ports", protocol: "-1", from: -1, to: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSecurityRule(&SecurityRule{Protocol: tt.protocol, FromPort: tt.from, ToPort: tt.to})

			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				return
			}

			if !cerrors.IsInvalidArgument(err) {
				t.Fatalf("err = %v, want InvalidArgument", err)
			}
		})
	}
}
