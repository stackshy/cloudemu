package sqs

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/awspolicy"
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

	if doc.Has(label) {
		return errors.Newf(errors.InvalidArgument, "label %q already exists", label)
	}

	doc.Statement = append(doc.Statement, awspolicy.Statement{
		Sid:       label,
		Effect:    "Allow",
		Principal: awspolicy.AccountRootPrincipals(accountIDs),
		Action:    awspolicy.QualifyActions("SQS:", actions),
		Resource:  qd.info.ARN,
	})

	encoded, err := doc.Encode()
	if err != nil {
		return errors.Newf(errors.Internal, "encode policy: %v", err)
	}

	qd.policy = encoded
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

	doc, err := awspolicy.Decode(qd.policy)
	if err != nil {
		return errors.Newf(errors.InvalidArgument, "queue policy is not valid JSON: %v", err)
	}

	if !doc.Remove(label) {
		return errors.Newf(errors.InvalidArgument, "label %q not found", label)
	}

	encoded, err := doc.Encode()
	if err != nil {
		return errors.Newf(errors.Internal, "encode policy: %v", err)
	}

	qd.policy = encoded
	qd.lastModifiedAt = m.opts.Clock.Now()

	return nil
}

// queuePolicyDoc returns the queue's stored policy as a decoded document, or a
// fresh default document when none is stored (matching SQS AddPermission, which
// seeds a default policy id derived from the queue ARN).
func queuePolicyDoc(qd *queueData) (*awspolicy.Document, error) {
	if qd.policy != "" {
		return awspolicy.Decode(qd.policy)
	}

	return awspolicy.NewDefault(qd.info.ARN + "/SQSDefaultPolicy"), nil
}
