package iam

import (
	"testing"
)

// TestActionWildcardAnchored covers the Action and NotAction glob rules: '*' is
// any run (including empty), '?' is exactly one character, the pattern is
// anchored at both ends, and action names compare case-insensitively.
func TestActionWildcardAnchored(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		action  string
		allowed bool
	}{
		{"suffix star matches", "s3:*Bucket", "s3:CreateBucket", true},
		{"suffix star is anchored", "s3:*Bucket", "s3:DeleteBucketPolicy", false},
		{"suffix star anchored mid-string", "s3:*Bucket", "s3:GetBucketTagging", false},
		{"star matches empty", "s3:Get*", "s3:Get", true},
		{"star alone in middle matches empty", "s3:Get*Object", "s3:GetObject", true},
		{"question mark one char", "s3:Get?bject", "s3:GetObject", true},
		{"question mark not zero chars", "s3:Get?Object", "s3:GetObject", false},
		{"question mark not two chars", "s3:Get?bject", "s3:GetXXbject", false},
		{"exact literal", "s3:GetObject", "s3:GetObject", true},
		{"literal is anchored", "s3:GetObject", "s3:GetObjectAcl", false},
		{"action case-insensitive", "S3:getobject", "s3:GetObject", true},
		{"action wildcard case-insensitive", "dynamodb:*table", "dynamodb:DescribeTable", true},
		{"backtracking star", "s3:*Object*Acl", "s3:PutObjectVersionAcl", true},
		{"backtracking star anchored", "s3:*Object*Acl", "s3:PutObjectAclX", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := makePolicyDoc([]map[string]any{
				{"Effect": "Allow", "Action": tc.pattern, "Resource": "*"},
			})

			want := decisionImplicitDeny
			if tc.allowed {
				want = decisionAllowed
			}

			assertEqual(t, want, decideDoc(doc, tc.action, "*", nil))
		})
	}
}

// TestNotActionWildcardAnchored proves NotAction uses the same anchored glob:
// an action that only contains the pattern text mid-string is not exempted.
func TestNotActionWildcardAnchored(t *testing.T) {
	doc := makePolicyDoc([]map[string]any{
		{"Effect": "Allow", "Action": "s3:*", "Resource": "*"},
		{"Effect": "Deny", "NotAction": "s3:*Bucket", "Resource": "*"},
	})

	assertEqual(t, decisionAllowed, decideDoc(doc, "s3:CreateBucket", "*", nil))
	assertEqual(t, decisionExplicitDeny, decideDoc(doc, "s3:DeleteBucketPolicy", "*", nil))
}

// TestResourceWildcardAnchored covers Resource and NotResource matching: same
// glob grammar as Action, but ARNs compare case-sensitively.
func TestResourceWildcardAnchored(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		resource string
		allowed  bool
	}{
		{"object under bucket", "arn:aws:s3:::b/*", "arn:aws:s3:::b/key.txt", true},
		{"nested object under bucket", "arn:aws:s3:::b/*", "arn:aws:s3:::b/dir/key.txt", true},
		{"empty key still matches star", "arn:aws:s3:::b/*", "arn:aws:s3:::b/", true},
		{"bucket itself is not an object", "arn:aws:s3:::b/*", "arn:aws:s3:::b", false},
		{"other bucket with same prefix", "arn:aws:s3:::b/*", "arn:aws:s3:::bb/key", false},
		{"literal is anchored", "arn:aws:s3:::b", "arn:aws:s3:::bucket", false},
		{"suffix pattern anchored", "arn:aws:s3:::*-logs", "arn:aws:s3:::app-logs-old", false},
		{"suffix pattern matches", "arn:aws:s3:::*-logs", "arn:aws:s3:::app-logs", true},
		{"question mark in resource", "arn:aws:s3:::b?", "arn:aws:s3:::b1", true},
		{"question mark exactly one", "arn:aws:s3:::b?", "arn:aws:s3:::b12", false},
		{"resource case-sensitive", "arn:aws:s3:::Bucket/*", "arn:aws:s3:::bucket/x", false},
		{"resource star spans colons", "arn:aws:dynamodb:*", "arn:aws:dynamodb:us-east-1:123456789012:table/t", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := makePolicyDoc([]map[string]any{
				{"Effect": "Allow", "Action": "s3:GetObject", "Resource": tc.pattern},
			})

			want := decisionImplicitDeny
			if tc.allowed {
				want = decisionAllowed
			}

			assertEqual(t, want, decideDoc(doc, "s3:GetObject", tc.resource, nil))
		})
	}
}

// TestNotResourceWildcardAnchored proves NotResource exempts only the exact
// glob match.
func TestNotResourceWildcardAnchored(t *testing.T) {
	doc := makePolicyDoc([]map[string]any{
		{"Effect": "Allow", "Action": "s3:*", "Resource": "*"},
		{"Effect": "Deny", "Action": "s3:*", "NotResource": "arn:aws:s3:::safe"},
	})

	assertEqual(t, decisionAllowed, decideDoc(doc, "s3:GetObject", "arn:aws:s3:::safe", nil))
	assertEqual(t, decisionExplicitDeny, decideDoc(doc, "s3:GetObject", "arn:aws:s3:::safe-not", nil))
}

// TestStringLikeArnLikeGlob covers the StringLike and ArnLike condition
// operators: anchored glob with '*' and '?', case-sensitive, and ArnLike checks
// each of the six ARN components on its own.
func TestStringLikeArnLikeGlob(t *testing.T) {
	const roleArn = "arn:aws:iam::123456789012:role/app"

	tests := []struct {
		name    string
		op      string
		pattern string
		value   string
		allowed bool
	}{
		{"StringLike suffix anchored", "StringLike", "*-prod", "app-prod-old", false},
		{"StringLike suffix match", "StringLike", "*-prod", "app-prod", true},
		{"StringLike question mark", "StringLike", "user-?", "user-1", true},
		{"StringLike question mark exactly one", "StringLike", "user-?", "user-12", false},
		{"StringLike star matches empty", "StringLike", "home/*", "home/", true},
		{"StringLike case-sensitive", "StringLike", "Bob*", "bob", false},
		{"StringNotLike anchored", "StringNotLike", "*-prod", "app-prod-old", true},
		{"ArnLike wildcard account", "ArnLike", "arn:aws:iam::*:role/app", roleArn, true},
		{"ArnLike resource anchored", "ArnLike", "arn:aws:iam::*:role/app", roleArn + "-admin", false},
		{"ArnLike star stays in its segment", "ArnLike", "arn:aws:iam::*", roleArn, false},
		{"ArnLike star cannot swallow segments", "ArnLike", "arn:aws:*:role/app", roleArn, false},
		{"ArnLike star per segment", "ArnLike", "arn:aws:*:*:*:*", roleArn, true},
		{"ArnLike question mark in segment", "ArnLike", "arn:aws:iam::12345678901?:role/app", roleArn, true},
		{"ArnLike case-sensitive", "ArnLike", "arn:aws:iam::*:role/App", roleArn, false},
		{"ArnLike resource keeps colons", "ArnLike", "arn:aws:logs:*:*:log-group:g:*",
			"arn:aws:logs:us-east-1:123456789012:log-group:g:log-stream:s", true},
		{"ArnLike value not an ARN", "ArnLike", "arn:aws:iam::*:role/app", "role/app", false},
		{"ArnEquals segment wildcard", "ArnEquals", "arn:aws:iam::*:role/app", roleArn, true},
		{"ArnNotLike anchored", "ArnNotLike", "arn:aws:iam::*:role/app", roleArn + "-admin", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := makePolicyDoc([]map[string]any{{
				"Effect":    "Allow",
				"Action":    "s3:GetObject",
				"Resource":  "*",
				"Condition": map[string]any{tc.op: map[string]any{"aws:PrincipalArn": tc.pattern}},
			}})

			want := decisionImplicitDeny
			if tc.allowed {
				want = decisionAllowed
			}

			assertEqual(t, want, decideDoc(doc, "s3:GetObject", "*", ConditionContext{"aws:PrincipalArn": tc.value}))
		})
	}
}

// TestTrustPrincipalWildcardAnchored proves a wildcard principal in a trust
// policy does not match a caller that merely starts with the pattern.
func TestTrustPrincipalWildcardAnchored(t *testing.T) {
	assertEqual(t, true, principalEntryMatches("arn:aws:iam::123456789012:role/app-*", "arn:aws:iam::123456789012:role/app-x"))
	assertEqual(t, false, principalEntryMatches("arn:aws:iam::123456789012:*/app", "arn:aws:iam::123456789012:role/app-admin"))
	assertEqual(t, false, principalEntryMatches("arn:aws:iam::123456789012:role/*", "arn:aws:iam::123456789012:user/x"))
}

// TestServiceWideActionMatch covers the service-wide simulation helpers, which
// reason about whole services rather than one action.
func TestServiceWideActionMatch(t *testing.T) {
	covers := map[string]bool{
		"*": true, "s3:*": true, "S3:*": true, "s*": true, "s?:*": true,
		"s3:Get*": false, "s3:?": false, "s3:*Bucket": false, "ec2:*": false,
	}

	for p, want := range covers {
		assertEqual(t, want, coversService(p, "s3"))
	}

	could := map[string]bool{
		"s3:Get*": true, "S3:GetObject": true, "s?:x": true, "s3?GetObject": true,
		"*Bucket": true, "ec2:*": false, "s3": false, "x?": false,
	}

	for p, want := range could {
		assertEqual(t, want, couldMatchService(p, "s3"))
	}
}

// TestConservativeModesNeverWiden checks, over a grid of wildcard patterns, that
// the unknown-resource and service-wide answers never allow something a
// concrete evaluation denies, and never miss a Deny a concrete evaluation hits.
func TestConservativeModesNeverWiden(t *testing.T) {
	patterns := []string{
		"*", "s3:*", "s3:*Bucket", "s3:Get?bject", "s?:*", "s3:?", "S3:get*", "s*", "*Bucket", "s3:*Object*",
	}
	actions := []string{"s3:GetObject", "s3:CreateBucket", "s3:DeleteBucketPolicy", "s3:PutObjectAcl", "s3:X"}
	resources := []string{"*", "arn:aws:s3:::b", "arn:aws:s3:::b/k"}

	for _, p := range patterns {
		for _, field := range []string{"Action", "NotAction"} {
			allowDoc := makePolicyDoc([]map[string]any{{"Effect": "Allow", field: p, "Resource": "*"}})
			denyDoc := makePolicyDoc([]map[string]any{
				{"Effect": "Allow", "Action": "*", "Resource": "*"},
				{"Effect": "Deny", field: p, "Resource": "arn:aws:s3:::b"},
			})

			for _, doc := range []string{allowDoc, denyDoc} {
				checkConservative(t, doc, actions, resources)
			}
		}
	}
}

func checkConservative(t *testing.T, doc string, actions, resources []string) {
	t.Helper()

	docs := []string{doc}
	wide := decideWith(docs, evalRequest{service: "s3"}, evalServiceWide)

	for _, a := range actions {
		unknown := decideWith(docs, evalRequest{action: a}, evalUnknownResource)

		for _, r := range resources {
			known := decide(docs, a, r, nil)

			if unknown == decisionAllowed && known != decisionAllowed {
				t.Errorf("unknown-resource allows %s but %s on %s is %s: %s", a, a, r, known, doc)
			}

			if known == decisionExplicitDeny && unknown != decisionExplicitDeny {
				t.Errorf("unknown-resource misses deny of %s on %s: %s", a, r, doc)
			}

			if wide == decisionAllowed && known != decisionAllowed {
				t.Errorf("service-wide allows s3 but %s on %s is %s: %s", a, r, known, doc)
			}

			if known == decisionExplicitDeny && wide != decisionExplicitDeny {
				t.Errorf("service-wide misses deny of %s on %s: %s", a, r, doc)
			}
		}
	}
}

// TestGlobMatchEdges pins the matcher's corner cases directly.
func TestGlobMatchEdges(t *testing.T) {
	assertEqual(t, true, globMatch("*", ""))
	assertEqual(t, true, globMatch("**", ""))
	assertEqual(t, true, globMatch("", ""))
	assertEqual(t, false, globMatch("", "a"))
	assertEqual(t, false, globMatch("?", ""))
	assertEqual(t, true, globMatch("a*b*c", "abc"))
	assertEqual(t, true, globMatch("a*b*c", "aXbYbZc"))
	assertEqual(t, false, globMatch("a*b*c", "aXbYc-"))
	assertEqual(t, true, globMatch("?", "é"))
	assertEqual(t, true, arnMatch("anything", "*"))
	assertEqual(t, false, actionMatch("s3:*Bucket", "s3:DeleteBucketPolicy"))
}
