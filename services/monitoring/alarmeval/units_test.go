package alarmeval_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/stackshy/cloudemu/v2/services/monitoring/alarmeval"
)

func TestValidUnit(t *testing.T) {
	for _, u := range []string{"Percent", "Count", "None", "Bytes/Second", "Count/Second", "Terabits"} {
		assert.True(t, alarmeval.ValidUnit(u), u)
	}

	for _, u := range []string{"", "Parsecs", "percent", "Bytes/Sec"} {
		assert.False(t, alarmeval.ValidUnit(u), u)
	}

	assert.Len(t, alarmeval.Units(), 27)
}

func TestMatchUnit(t *testing.T) {
	tests := []struct {
		name  string
		datum string
		want  string
		match bool
	}{
		{name: "no filter matches a unit", datum: "Percent", want: "", match: true},
		{name: "no filter matches no unit", datum: "", want: "", match: true},
		{name: "exact", datum: "Percent", want: "Percent", match: true},
		{name: "different unit", datum: "Percent", want: "Bytes", match: false},
		{name: "unit-less datum is None", datum: "", want: "None", match: true},
		{name: "unit-less datum is not Count", datum: "", want: "Count", match: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.match, alarmeval.MatchUnit(tc.datum, tc.want))
		})
	}

	assert.Equal(t, "None", alarmeval.EffectiveUnit(""))
	assert.Equal(t, "Seconds", alarmeval.EffectiveUnit("Seconds"))
}
