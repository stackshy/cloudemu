package sqs

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/errors"
)

// AddPermission adds an Allow statement (Sid=label) granting the given accounts
// the given SQS actions on the queue, mutating the stored access policy. It is
// AWS-specific and used directly by the SQS wire handler.
func (m *Mock) AddPermission(_ context.Context, queueURL, label string, accountIDs, actions []string) error {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return errors.Newf(errors.NotFound, "queue %q not found", queueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	doc, err := queuePolicyDoc(qd)
	if err != nil {
		return errors.Newf(errors.InvalidArgument, "queue policy is not valid JSON: %v", err)
	}

	for i := range doc.Statement {
		if doc.Statement[i].Sid == label {
			return errors.Newf(errors.InvalidArgument, "label %q already exists", label)
		}
	}

	principals := make([]string, 0, len(accountIDs))
	for _, acct := range accountIDs {
		principals = append(principals, "arn:aws:iam::"+acct+":root")
	}

	qualified := make([]string, 0, len(actions))
	for _, a := range actions {
		qualified = append(qualified, "SQS:"+a)
	}

	doc.Statement = append(doc.Statement, policyStatement{
		Sid:       label,
		Effect:    "Allow",
		Principal: map[string]any{"AWS": principals},
		Action:    qualified,
		Resource:  qd.info.ARN,
	})

	encoded, err := json.Marshal(doc)
	if err != nil {
		return errors.Newf(errors.Internal, "encode policy: %v", err)
	}

	qd.policy = string(encoded)
	qd.lastModifiedAt = m.opts.Clock.Now()

	return nil
}

// RemovePermission removes the statement whose Sid equals label. It is
// AWS-specific and used directly by the SQS wire handler.
func (m *Mock) RemovePermission(_ context.Context, queueURL, label string) error {
	qd, ok := m.queues.Get(queueURL)
	if !ok {
		return errors.Newf(errors.NotFound, "queue %q not found", queueURL)
	}

	qd.mu.Lock()
	defer qd.mu.Unlock()

	if qd.policy == "" {
		return errors.Newf(errors.InvalidArgument, "label %q not found", label)
	}

	doc, err := decodeQueuePolicy(qd.policy)
	if err != nil {
		return errors.Newf(errors.InvalidArgument, "queue policy is not valid JSON: %v", err)
	}

	kept := doc.Statement[:0]
	removed := false

	for _, st := range doc.Statement {
		if st.Sid == label {
			removed = true
			continue
		}

		kept = append(kept, st)
	}

	if !removed {
		return errors.Newf(errors.InvalidArgument, "label %q not found", label)
	}

	doc.Statement = kept

	encoded, err := json.Marshal(doc)
	if err != nil {
		return errors.Newf(errors.Internal, "encode policy: %v", err)
	}

	qd.policy = string(encoded)
	qd.lastModifiedAt = m.opts.Clock.Now()

	return nil
}

// policyDoc / policyStatement model just enough of an SQS access policy to add
// and remove statements while round-tripping unknown fields verbatim.
type policyDoc struct {
	Version   string            `json:"Version"`
	ID        string            `json:"Id,omitempty"`
	Statement []policyStatement `json:"Statement"`
}

type policyStatement struct {
	Sid       string         `json:"Sid,omitempty"`
	Effect    string         `json:"Effect"`
	Principal any            `json:"Principal,omitempty"`
	Action    any            `json:"Action,omitempty"`
	Resource  any            `json:"Resource,omitempty"`
	Condition map[string]any `json:"Condition,omitempty"`
}

func decodeQueuePolicy(s string) (*policyDoc, error) {
	var doc policyDoc
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		return nil, err
	}

	return &doc, nil
}

// queuePolicyDoc returns the queue's stored policy as a decoded document, or a
// fresh default document when none is stored (matching SQS AddPermission, which
// seeds a default policy id derived from the queue ARN).
func queuePolicyDoc(qd *queueData) (*policyDoc, error) {
	if qd.policy != "" {
		return decodeQueuePolicy(qd.policy)
	}

	return &policyDoc{
		Version:   "2008-10-17",
		ID:        qd.info.ARN + "/SQSDefaultPolicy",
		Statement: []policyStatement{},
	}, nil
}
