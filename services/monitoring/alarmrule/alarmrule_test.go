package alarmrule

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func states(m map[string]string) func(string) string {
	return func(ref string) string {
		if s, ok := m[ref]; ok {
			return s
		}

		return StateInsufficientData
	}
}

func TestParseAndEval(t *testing.T) {
	tests := []struct {
		name   string
		rule   string
		states map[string]string
		want   bool
		refs   []string
	}{
		{"and both alarm", "ALARM(A) AND ALARM(B)", map[string]string{"A": "ALARM", "B": "ALARM"}, true, []string{"A", "B"}},
		{"and one alarm", "ALARM(A) AND ALARM(B)", map[string]string{"A": "ALARM", "B": "OK"}, false, []string{"A", "B"}},
		{"and not", "ALARM(A) AND NOT ALARM(B)", map[string]string{"A": "ALARM", "B": "OK"}, true, []string{"A", "B"}},
		{"grouped or with ok", "(ALARM(A) OR ALARM(B)) AND OK(C)", map[string]string{"B": "ALARM", "C": "OK"}, true, []string{"A", "B", "C"}},
		{"grouped or with ok fails", "(ALARM(A) OR ALARM(B)) AND OK(C)", map[string]string{"B": "ALARM", "C": "ALARM"}, false, nil},
		{"true", "TRUE", nil, true, []string{}},
		{"false", "FALSE", nil, false, []string{}},
		{"quoted names", `ALARM("cpu high") OR OK("disk")`, map[string]string{"disk": "OK"}, true, []string{"cpu high", "disk"}},
		{"arn ref", "ALARM(arn:aws:cloudwatch:us-east-1:123456789012:alarm:cpu)",
			map[string]string{"arn:aws:cloudwatch:us-east-1:123456789012:alarm:cpu": "ALARM"}, true,
			[]string{"arn:aws:cloudwatch:us-east-1:123456789012:alarm:cpu"}},
		{"insufficient data", "INSUFFICIENT_DATA(x)", nil, true, []string{"x"}},
		// NOT binds tighter than AND: (NOT A) AND B.
		{"not precedence", "NOT ALARM(A) AND ALARM(B)", map[string]string{"A": "ALARM", "B": "ALARM"}, false, nil},
		// AND binds tighter than OR: A OR (B AND C).
		{"or precedence", "ALARM(A) OR ALARM(B) AND ALARM(C)", map[string]string{"A": "ALARM"}, true, nil},
		{"double not", "NOT NOT TRUE", nil, true, []string{}},
		{"repeated ref once", "ALARM(A) OR OK(A)", map[string]string{"A": "OK"}, true, []string{"A"}},
		{"extra spaces", "  ALARM( a )   AND\tOK(\"b\")  ", map[string]string{"a": "ALARM", "b": "OK"}, true, []string{"a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := Parse(tt.rule)
			require.NoError(t, err)
			assert.Equal(t, tt.want, r.Eval(states(tt.states)))

			if tt.refs != nil {
				assert.Equal(t, tt.refs, r.Refs())
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		rule string
	}{
		{"empty", ""},
		{"lowercase function", "alarm(x)"},
		{"unknown function", "WARN(x)"},
		{"unbalanced open", "(ALARM(x)"},
		{"unbalanced close", "ALARM(x))"},
		{"missing operand", "ALARM(x) AND"},
		{"trailing token", "ALARM(x) ALARM(y)"},
		{"empty ref", "ALARM()"},
		{"empty quoted ref", `ALARM("")`},
		{"unterminated quote", `ALARM("x)`},
		{"no call parens", "ALARM x"},
		{"glued keyword", "ALARM(x)ANDALARM(y)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.rule)
			assert.Error(t, err)
		})
	}
}

func joinCalls(n int, prefix string) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprintf("ALARM(%s%d)", prefix, i)
	}

	return strings.Join(parts, " OR ")
}

func TestParseLimits(t *testing.T) {
	_, err := Parse(joinCalls(MaxChildren, "a"))
	require.NoError(t, err)

	_, err = Parse(joinCalls(MaxChildren+1, "a"))
	assert.ErrorContains(t, err, "at most 100 alarms")

	// 501 elements over 100 distinct children.
	parts := make([]string, MaxElements+1)
	for i := range parts {
		parts[i] = fmt.Sprintf("ALARM(a%d)", i%MaxChildren)
	}

	_, err = Parse(strings.Join(parts, " OR "))
	assert.ErrorContains(t, err, "at most 500 elements")

	_, err = Parse("ALARM(" + strings.Repeat("a", MaxLength) + ")")
	assert.ErrorContains(t, err, "at most 10240 characters")
}
