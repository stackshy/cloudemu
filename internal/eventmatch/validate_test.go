package eventmatch_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/internal/eventmatch"
)

func TestValidatePattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		wantErr bool
	}{
		{"array leaf", `{"source":["aws.ec2"]}`, false},
		{"multiple array leaves", `{"source":["a"],"detail-type":["b"]}`, false},
		{"nested detail object", `{"detail":{"state":["running"]}}`, false},
		{"content operator", `{"source":[{"prefix":"aws."}]}`, false},
		{"or clause", `{"$or":[{"source":["a"]},{"detail-type":["b"]}]}`, false},
		{"scalar string leaf", `{"source":"not-an-array"}`, true},
		{"scalar number leaf", `{"detail-type":123}`, true},
		{"nested scalar leaf", `{"detail":{"state":"running"}}`, true},
		{"malformed json", `{not json`, true},
		{"non-object top level", `["a"]`, true},
		{"empty", ``, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := eventmatch.ValidatePattern(tt.pattern)
			if tt.wantErr && err == nil {
				t.Fatalf("ValidatePattern(%s) = nil, want error", tt.pattern)
			}

			if !tt.wantErr && err != nil {
				t.Fatalf("ValidatePattern(%s) = %v, want nil", tt.pattern, err)
			}
		})
	}
}
