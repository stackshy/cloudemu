package driver

import (
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

func TestValidateSecurityRule(t *testing.T) {
	tests := []struct {
		name     string
		from, to int
		wantErr  bool
	}{
		{name: "single port", from: 22, to: 22},
		{name: "full range", from: 0, to: 65535},
		{name: "to above max", from: 0, to: 99999, wantErr: true},
		{name: "negative from", from: -1, to: 80, wantErr: true},
		{name: "from greater than to", from: 443, to: 80, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSecurityRule(&SecurityRule{Protocol: "tcp", FromPort: tt.from, ToPort: tt.to})

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
