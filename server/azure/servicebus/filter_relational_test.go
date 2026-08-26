package servicebus

import "testing"

// TestSQLRelationalStringOperands guards the message-loss regression where a
// relational SqlFilter (<, >, <=, >=) on a string or date operand dropped every
// message because the operands weren't both numeric. Real Service Bus evaluates
// these lexically and delivers, so string operands must compare lexically while
// numeric operands keep comparing numerically, and an unparseable clause must
// still fall through to a match (the no-drop invariant).
func TestSQLRelationalStringOperands(t *testing.T) {
	tests := []struct {
		name string
		expr string
		p    *messageProps
		want bool
	}{
		{"string greater matches", "sys.Label > 'm'", &messageProps{Label: "z"}, true},
		{"string greater no match", "sys.Label > 'm'", &messageProps{Label: "a"}, false},
		{"string ge boundary equal", "sys.Label >= 'a'", &messageProps{Label: "a"}, true},
		{"string ge boundary below", "sys.Label >= 'b'", &messageProps{Label: "a"}, false},
		{"date greater matches", "mydate > '2024-01-01'", &messageProps{Custom: map[string]string{"mydate": "2024-06-01"}}, true},
		{"numeric greater matches", "x > 5", &messageProps{Custom: map[string]string{"x": "10"}}, true},
		{"numeric greater no match", "x > 5", &messageProps{Custom: map[string]string{"x": "3"}}, false},
		{"unparseable falls to match", "this is not sql", &messageProps{Label: "z"}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := evalSQLExpression(tc.expr, tc.p); got != tc.want {
				t.Fatalf("evalSQLExpression(%q) = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}
