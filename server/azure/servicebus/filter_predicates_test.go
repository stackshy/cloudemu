package servicebus

import "testing"

// TestSQLFilterSetPredicates covers the LIKE / IN / EXISTS / IS NULL SqlFilter
// predicates that previously fell back to the no-drop match=true path and so
// over-delivered every message. Real Service Bus delivers only matching
// messages, so a non-matching predicate must now exclude, while a genuinely
// unsupported clause keeps the safe fallback.
func TestSQLFilterSetPredicates(t *testing.T) {
	tests := []struct {
		name string
		expr string
		p    *messageProps
		want bool
	}{
		// LIKE with % (any run) and _ (single char), case-sensitive.
		{"like percent matches", "sys.Label LIKE 'order-%'", &messageProps{Label: "order-1"}, true},
		{"like percent no match", "sys.Label LIKE 'order-%'", &messageProps{Label: "shipment-1"}, false},
		{"like underscore matches", "sys.Label LIKE 'a_c'", &messageProps{Label: "abc"}, true},
		{"like underscore too long", "sys.Label LIKE 'a_c'", &messageProps{Label: "abbc"}, false},
		{"like case sensitive", "sys.Label LIKE 'Order-%'", &messageProps{Label: "order-1"}, false},
		{"not like excludes match", "sys.Label NOT LIKE 'order-%'", &messageProps{Label: "order-1"}, false},
		{"not like keeps non match", "sys.Label NOT LIKE 'order-%'", &messageProps{Label: "shipment"}, true},
		{"like on custom prop", "region LIKE 'us-%'", &messageProps{Custom: map[string]string{"region": "us-east"}}, true},

		// IN / NOT IN membership over strings and numbers.
		{"in matches", "user.Region IN ('us','eu')", &messageProps{Custom: map[string]string{"Region": "eu"}}, true},
		{"in no match", "user.Region IN ('us','eu')", &messageProps{Custom: map[string]string{"Region": "ap"}}, false},
		{"in numeric matches", "priority IN (1,3,5)", &messageProps{Custom: map[string]string{"priority": "3"}}, true},
		{"in numeric no match", "priority IN (1,3,5)", &messageProps{Custom: map[string]string{"priority": "2"}}, false},
		{"not in excludes member", "user.Region NOT IN ('us','eu')", &messageProps{Custom: map[string]string{"Region": "us"}}, false},
		{"not in keeps non member", "user.Region NOT IN ('us','eu')", &messageProps{Custom: map[string]string{"Region": "ap"}}, true},
		{"in absent prop", "user.Region IN ('us')", &messageProps{}, false},

		// IS NULL / IS NOT NULL presence tests.
		{"is null on absent", "user.Region IS NULL", &messageProps{}, true},
		{"is null on present", "user.Region IS NULL", &messageProps{Custom: map[string]string{"Region": "us"}}, false},
		{"is not null on present", "user.Region IS NOT NULL", &messageProps{Custom: map[string]string{"Region": "us"}}, true},
		{"is not null on absent", "user.Region IS NOT NULL", &messageProps{}, false},

		// EXISTS / NOT EXISTS presence tests.
		{"exists present", "EXISTS(user.Region)", &messageProps{Custom: map[string]string{"Region": "us"}}, true},
		{"exists absent", "EXISTS(user.Region)", &messageProps{}, false},
		{"not exists absent", "NOT EXISTS(user.Region)", &messageProps{}, true},
		{"not exists present", "NOT EXISTS(user.Region)", &messageProps{Custom: map[string]string{"Region": "us"}}, false},

		// Predicates compose with AND/OR and parens.
		{"like and in", "sys.Label LIKE 'o%' AND user.Region IN ('us')",
			&messageProps{Label: "order", Custom: map[string]string{"Region": "us"}}, true},
		{"like or exists", "sys.Label LIKE 'z%' OR EXISTS(user.Region)",
			&messageProps{Label: "order", Custom: map[string]string{"Region": "us"}}, true},

		// A clause the grammar still cannot parse keeps the safe no-drop path.
		{"incomplete clause falls to match", "sys.Label", &messageProps{Label: "anything"}, true},
		{"non-sql falls to match", "totally not sql", &messageProps{Label: "anything"}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := evalSQLExpression(tc.expr, tc.p); got != tc.want {
				t.Fatalf("evalSQLExpression(%q) = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

// TestSQLLikeWildcards exercises the LIKE matcher directly, including anchoring
// and multi-wildcard patterns.
func TestSQLLikeWildcards(t *testing.T) {
	tests := []struct {
		value, pattern string
		want           bool
	}{
		{"order-123", "order-%", true},
		{"order-123", "%-123", true},
		{"order-123", "%rder%", true},
		{"order", "order", true},
		{"order", "orders", false},
		{"order", "ord_r", true},
		{"order", "ord__r", false},
		{"", "%", true},
		{"anything", "%", true},
		{"a", "_", true},
		{"", "_", false},
	}

	for _, tc := range tests {
		if got := sqlLike(tc.value, tc.pattern); got != tc.want {
			t.Fatalf("sqlLike(%q, %q) = %v, want %v", tc.value, tc.pattern, got, tc.want)
		}
	}
}
