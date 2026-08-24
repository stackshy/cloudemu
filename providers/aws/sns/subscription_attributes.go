package sns

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/awspolicy"
	"github.com/stackshy/cloudemu/v2/services/notification/driver"
)

// mutableSubAttributes is the set of subscription attributes a caller may set via
// SetSubscriptionAttributes. Real SNS rejects unknown attribute names.
var mutableSubAttributes = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	"DeliveryPolicy":      {},
	"FilterPolicy":        {},
	"FilterPolicyScope":   {},
	"RawMessageDelivery":  {},
	"RedrivePolicy":       {},
	"SubscriptionRoleArn": {},
}

// findSubscription locates a subscription by its ARN across every topic and
// returns its owning topic plus a copy of the subscription.
func (m *Mock) findSubscription(subscriptionARN string) (*topicData, driver.SubscriptionInfo, bool) {
	for _, td := range m.topics.All() {
		if sub, ok := td.subscriptions.Get(subscriptionARN); ok {
			return td, sub, true
		}
	}

	return nil, driver.SubscriptionInfo{}, false
}

// GetSubscription returns a copy of the subscription identified by its ARN.
func (m *Mock) GetSubscription(_ context.Context, subscriptionARN string) (*driver.SubscriptionInfo, error) {
	_, sub, ok := m.findSubscription(subscriptionARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "subscription %q not found", subscriptionARN)
	}

	result := sub

	return &result, nil
}

// SetSubscriptionAttribute sets a single subscription attribute to value,
// mirroring SNS SetSubscriptionAttributes (one attribute per call).
func (m *Mock) SetSubscriptionAttribute(_ context.Context, subscriptionARN, name, value string) error {
	if _, ok := mutableSubAttributes[name]; !ok {
		return errors.Newf(errors.InvalidArgument, "AttributeName %q is not a valid subscription attribute", name)
	}

	td, sub, ok := m.findSubscription(subscriptionARN)
	if !ok {
		return errors.Newf(errors.NotFound, "subscription %q not found", subscriptionARN)
	}

	if sub.Attributes == nil {
		sub.Attributes = make(map[string]string, 1)
	}

	if value == "" {
		delete(sub.Attributes, name)
	} else {
		sub.Attributes[name] = value
	}

	td.subscriptions.Set(subscriptionARN, sub)

	return nil
}

// ConfirmSubscription confirms a pending subscription on the topic. The token
// must match the pending subscription's confirmation token. It returns the
// confirmed subscription's ARN.
func (m *Mock) ConfirmSubscription(_ context.Context, topicID, token string) (*driver.SubscriptionInfo, error) {
	if token == "" {
		return nil, errors.New(errors.InvalidArgument, "confirmation token is required")
	}

	td, ok := m.topics.Get(topicID)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "topic %q not found", topicID)
	}

	for _, sub := range td.subscriptions.All() {
		if sub.Status != statusPending || sub.ConfirmationToken != token {
			continue
		}

		sub.Status = statusConfirmed
		sub.ConfirmationToken = ""
		td.subscriptions.Set(sub.ID, sub)

		result := sub

		return &result, nil
	}

	return nil, errors.New(errors.InvalidArgument, "Invalid token")
}

// AddTopicPermission adds an Allow statement (Sid=label) granting the given
// accounts the given SNS actions on the topic, mutating the stored policy.
func (m *Mock) AddTopicPermission(_ context.Context, topicID, label string, accountIDs, actions []string) error {
	td, ok := m.topics.Get(topicID)
	if !ok {
		return errors.Newf(errors.NotFound, "topic %q not found", topicID)
	}

	doc, err := topicPolicyDoc(td)
	if err != nil {
		return errors.Newf(errors.InvalidArgument, "topic policy is not valid JSON: %v", err)
	}

	doc.Statement = append(doc.Statement, awspolicy.Statement{
		Sid:       label,
		Effect:    "Allow",
		Principal: awspolicy.AccountRootPrincipals(accountIDs),
		Action:    awspolicy.QualifyActions("SNS:", actions),
		Resource:  td.info.ResourceID,
	})

	encoded, err := doc.Encode()
	if err != nil {
		return errors.Newf(errors.Internal, "encode policy: %v", err)
	}

	td.info.Policy = encoded
	m.topics.Set(topicID, td)

	return nil
}

// RemoveTopicPermission removes the statement whose Sid equals label.
func (m *Mock) RemoveTopicPermission(_ context.Context, topicID, label string) error {
	td, ok := m.topics.Get(topicID)
	if !ok {
		return errors.Newf(errors.NotFound, "topic %q not found", topicID)
	}

	policy := td.info.Policy
	if policy == "" {
		return nil
	}

	doc, err := awspolicy.Decode(policy)
	if err != nil {
		return errors.Newf(errors.InvalidArgument, "topic policy is not valid JSON: %v", err)
	}

	doc.Remove(label)

	encoded, err := doc.Encode()
	if err != nil {
		return errors.Newf(errors.Internal, "encode policy: %v", err)
	}

	td.info.Policy = encoded
	m.topics.Set(topicID, td)

	return nil
}

// topicPolicyDoc returns the topic's stored policy as a decoded document, or a
// fresh default document (matching the SNS-seeded default) when none is stored.
func topicPolicyDoc(td *topicData) (*awspolicy.Document, error) {
	if td.info.Policy != "" {
		return awspolicy.Decode(td.info.Policy)
	}

	return awspolicy.NewDefault("__default_policy_ID"), nil
}
