package ec2

import (
	"slices"
	"testing"
)

// TestDeleteTagKeys covers how a DeleteTags request resolves against one
// resource's current tags, including the pass-through used when the owning
// provider cannot report tags (existing == nil).
func TestDeleteTagKeys(t *testing.T) {
	existing := map[string]string{"env": "prod", "blank": "", "aws:managed": "x"}

	cases := []struct {
		name     string
		existing map[string]string
		specs    []deleteTagSpec
		want     []string
		write    bool
	}{
		{"no Tag keeps aws: tags", existing, nil, []string{"blank", "env"}, true},
		{"key only", existing, []deleteTagSpec{{key: "env"}}, []string{"env"}, true},
		{"value match", existing, []deleteTagSpec{{key: "env", value: "prod", hasValue: true}}, []string{"env"}, true},
		{"value mismatch", existing, []deleteTagSpec{{key: "env", value: "dev", hasValue: true}}, nil, false},
		{"empty value matches only empty", existing, []deleteTagSpec{
			{key: "blank", hasValue: true}, {key: "env", hasValue: true},
		}, []string{"blank"}, true},
		{"absent key", existing, []deleteTagSpec{{key: "missing"}}, nil, false},
		{"no user tags left", map[string]string{"aws:managed": "x"}, nil, nil, false},
		{"unknown tags pass keys through", nil, []deleteTagSpec{{key: "env", value: "v", hasValue: true}}, []string{"env"}, true},
		{"unknown tags, no Tag", nil, nil, []string{}, true},
	}

	for _, tc := range cases {
		keys, write := deleteTagKeys(tc.existing, tc.specs)
		slices.Sort(keys)

		if write != tc.write || !slices.Equal(keys, tc.want) {
			t.Errorf("%s: deleteTagKeys = %v, %v; want %v, %v", tc.name, keys, write, tc.want, tc.write)
		}
	}
}
