package ecs

import (
	"encoding/json"
	"testing"
)

// TestListResponseEmptyArnsIsEmptyArrayNotNull guards that an empty (or nil)
// arns slice serializes to a JSON "[]", never "null". Real ECS always returns
// an array for a List op's ARN field, even when there are zero results; a
// caller iterating the field unconditionally (e.g. boto3) breaks on null.
// internal/pagination.Paginate returns a nil Items slice for an empty result,
// so listResponse must normalize that before it reaches encoding/json.
func TestListResponseEmptyArnsIsEmptyArrayNotNull(t *testing.T) {
	for name, arns := range map[string][]string{"nil slice": nil, "empty slice": {}} {
		t.Run(name, func(t *testing.T) {
			body, err := json.Marshal(listResponse("clusterArns", arns, ""))
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}

			got := string(body)
			if got != `{"clusterArns":[]}` {
				t.Fatalf("got %s, want {\"clusterArns\":[]}", got)
			}
		})
	}
}

// TestListResponseNonEmptyArnsPassThrough guards that a populated arns slice
// is unaffected by the nil-normalization.
func TestListResponseNonEmptyArnsPassThrough(t *testing.T) {
	body, err := json.Marshal(listResponse("clusterArns", []string{"arn:aws:ecs:us-east-1:000000000000:cluster/c1"}, ""))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	want := `{"clusterArns":["arn:aws:ecs:us-east-1:000000000000:cluster/c1"]}`
	if string(body) != want {
		t.Fatalf("got %s, want %s", body, want)
	}
}
