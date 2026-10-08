package iam

import "testing"

// TestNegatedOperatorMissingKey checks a negated operator is true when the
// condition key is missing from the request, and a positive one is false.
func TestNegatedOperatorMissingKey(t *testing.T) {
	cases := []struct {
		op   string
		want bool
	}{
		{"StringNotEquals", true},
		{"StringNotEqualsIgnoreCase", true},
		{"StringNotLike", true},
		{"ArnNotEquals", true},
		{"ArnNotLike", true},
		{"NumericNotEquals", true},
		{"DateNotEquals", true},
		{"NotIpAddress", true},
		{"StringEquals", false},
		{"StringLike", false},
		{"ArnLike", false},
		{"NumericEquals", false},
		{"StringEqualsIfExists", true},
		{"ForAnyValue:StringNotEquals", false},
		{"ForAllValues:StringNotEquals", true},
	}

	for _, tc := range cases {
		got := evaluateConditions(map[string]map[string]any{tc.op: {"sts:ExternalId": "x1"}}, nil)
		assertEqual(t, tc.want, got)
	}
}

// TestNegatedDenyAppliesWithoutTheKey covers the usual confused-deputy guard:
// a Deny with StringNotEquals on a key the request does not carry applies, in
// known-resource and unknown-resource evaluation alike.
func TestNegatedDenyAppliesWithoutTheKey(t *testing.T) {
	doc := makePolicyDoc([]map[string]any{
		{"Effect": "Allow", "Action": "s3:*", "Resource": "*"},
		{"Effect": "Deny", "Action": "s3:*", "Resource": "*",
			"Condition": map[string]any{"StringNotEquals": map[string]any{"aws:PrincipalTag/team": "blue"}}},
	})

	assertEqual(t, decisionExplicitDeny, decideDoc(doc, "s3:GetObject", "arn:aws:s3:::b/k", nil))
	assertEqual(t, decisionAllowed, decideDoc(doc, "s3:GetObject", "arn:aws:s3:::b/k",
		ConditionContext{"aws:PrincipalTag/team": "blue"}))

	unknown := decideWith([]string{doc}, evalRequest{action: "s3:GetObject"}, evalUnknownResource)
	assertEqual(t, decisionExplicitDeny, unknown)
}

// TestNegatedAllowWithoutTheKey checks an Allow guarded by a negated operator
// grants in known-resource evaluation when the key is missing, and that the
// conservative unknown-resource evaluation never grants more.
func TestNegatedAllowWithoutTheKey(t *testing.T) {
	doc := makePolicyDoc([]map[string]any{
		{"Effect": "Allow", "Action": "s3:*", "Resource": "*",
			"Condition": map[string]any{"StringNotEquals": map[string]any{"aws:PrincipalTag/team": "red"}}},
	})

	assertEqual(t, decisionAllowed, decideDoc(doc, "s3:GetObject", "arn:aws:s3:::b/k", nil))

	unknown := decideWith([]string{doc}, evalRequest{action: "s3:GetObject"}, evalUnknownResource)
	assertEqual(t, decisionImplicitDeny, unknown)
}
