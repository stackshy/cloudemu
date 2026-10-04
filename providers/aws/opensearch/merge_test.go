package opensearch

import (
	"encoding/json"
	"testing"
)

func TestMergeRawJSON(t *testing.T) {
	tests := []struct {
		name     string
		existing string
		incoming string
		want     string
	}{
		{"incoming adds keys", `{"a":1}`, `{"b":2}`, `{"a":1,"b":2}`},
		{"incoming overrides scalar", `{"a":1,"b":2}`, `{"b":3}`, `{"a":1,"b":3}`},
		{"nested objects merge", `{"o":{"x":1,"y":2}}`, `{"o":{"y":5}}`, `{"o":{"x":1,"y":5}}`},
		{"array replaces wholesale", `{"l":[1,2]}`, `{"l":[3]}`, `{"l":[3]}`},
		{"empty existing", ``, `{"a":1}`, `{"a":1}`},
		{"scalar incoming replaces", `{"a":1}`, `7`, `7`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeRawJSON(json.RawMessage(tc.existing), json.RawMessage(tc.incoming))

			var gotV, wantV any
			if err := json.Unmarshal(got, &gotV); err != nil {
				t.Fatalf("result %q is not JSON: %v", got, err)
			}

			if err := json.Unmarshal([]byte(tc.want), &wantV); err != nil {
				t.Fatalf("bad want: %v", err)
			}

			gb, _ := json.Marshal(gotV)
			wb, _ := json.Marshal(wantV)

			if string(gb) != string(wb) {
				t.Fatalf("merge = %s, want %s", gb, wb)
			}
		})
	}
}
