// Package awspolicy models just enough of an AWS IAM-style resource access
// policy (the JSON document behind SQS AddPermission, SNS AddPermission, and
// similar) to add and remove statements by Sid while round-tripping unknown
// fields verbatim. Provider mocks keep their own storage, locking, and
// per-service error semantics; this package is only the shared data model.
package awspolicy

import "encoding/json"

// defaultVersion is the policy language version AWS seeds a default resource
// policy with (SQS/SNS both use the 2008 version for the auto-created default).
const defaultVersion = "2008-10-17"

// Statement is one entry of a policy document's Statement array. Principal /
// Action / Resource are `any` so a caller's value (string or list) and any
// pre-existing shape round-trip unchanged.
type Statement struct {
	Sid       string         `json:"Sid,omitempty"`
	Effect    string         `json:"Effect"`
	Principal any            `json:"Principal,omitempty"`
	Action    any            `json:"Action,omitempty"`
	Resource  any            `json:"Resource,omitempty"`
	Condition map[string]any `json:"Condition,omitempty"`
}

// Document is a decoded access policy.
type Document struct {
	Version   string      `json:"Version"`
	ID        string      `json:"Id,omitempty"`
	Statement []Statement `json:"Statement"`
}

// Decode parses a stored policy JSON string.
func Decode(s string) (*Document, error) {
	var doc Document
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		return nil, err
	}

	return &doc, nil
}

// Encode serializes the document back to its JSON string form.
func (d *Document) Encode() (string, error) {
	b, err := json.Marshal(d)
	if err != nil {
		return "", err
	}

	return string(b), nil
}

// NewDefault returns a fresh empty policy with the given Id, matching the
// default document AWS seeds when a resource has no policy yet.
func NewDefault(id string) *Document {
	return &Document{Version: defaultVersion, ID: id, Statement: []Statement{}}
}

// Has reports whether a statement with the given Sid already exists.
func (d *Document) Has(sid string) bool {
	for i := range d.Statement {
		if d.Statement[i].Sid == sid {
			return true
		}
	}

	return false
}

// Remove drops the statement whose Sid equals sid, reporting whether one was
// removed.
func (d *Document) Remove(sid string) bool {
	kept := d.Statement[:0]
	removed := false

	for _, st := range d.Statement {
		if st.Sid == sid {
			removed = true
			continue
		}

		kept = append(kept, st)
	}

	d.Statement = kept

	return removed
}

// AccountRootPrincipals builds a Principal value granting the given AWS account
// ids (as account-root ARNs), the shape AddPermission produces.
func AccountRootPrincipals(accountIDs []string) map[string]any {
	principals := make([]string, 0, len(accountIDs))
	for _, acct := range accountIDs {
		principals = append(principals, "arn:aws:iam::"+acct+":root")
	}

	return map[string]any{"AWS": principals}
}

// QualifyActions prefixes each bare action with the service prefix (e.g. "SQS:"
// or "SNS:") that AddPermission requires.
func QualifyActions(prefix string, actions []string) []string {
	out := make([]string, 0, len(actions))
	for _, a := range actions {
		out = append(out, prefix+a)
	}

	return out
}
