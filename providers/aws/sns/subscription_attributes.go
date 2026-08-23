package sns

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/errors"
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

	principals := make([]string, 0, len(accountIDs))
	for _, acct := range accountIDs {
		principals = append(principals, "arn:aws:iam::"+acct+":root")
	}

	qualified := make([]string, 0, len(actions))
	for _, a := range actions {
		qualified = append(qualified, "SNS:"+a)
	}

	doc.Statement = append(doc.Statement, policyStatement{
		Sid:       label,
		Effect:    "Allow",
		Principal: map[string]any{"AWS": principals},
		Action:    qualified,
		Resource:  td.info.ResourceID,
	})

	encoded, err := json.Marshal(doc)
	if err != nil {
		return errors.Newf(errors.Internal, "encode policy: %v", err)
	}

	td.info.Policy = string(encoded)
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

	doc, err := decodePolicy(policy)
	if err != nil {
		return errors.Newf(errors.InvalidArgument, "topic policy is not valid JSON: %v", err)
	}

	kept := doc.Statement[:0]

	for _, st := range doc.Statement {
		if st.Sid != label {
			kept = append(kept, st)
		}
	}

	doc.Statement = kept

	encoded, err := json.Marshal(doc)
	if err != nil {
		return errors.Newf(errors.Internal, "encode policy: %v", err)
	}

	td.info.Policy = string(encoded)
	m.topics.Set(topicID, td)

	return nil
}

// policyDoc / policyStatement model just enough of an SNS access policy to add
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

func decodePolicy(s string) (*policyDoc, error) {
	var doc policyDoc
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		return nil, err
	}

	return &doc, nil
}

// topicPolicyDoc returns the topic's stored policy as a decoded document, or a
// fresh default document (matching the SNS-seeded default) when none is stored.
func topicPolicyDoc(td *topicData) (*policyDoc, error) {
	if td.info.Policy != "" {
		return decodePolicy(td.info.Policy)
	}

	return &policyDoc{
		Version:   "2008-10-17",
		ID:        "__default_policy_ID",
		Statement: []policyStatement{},
	}, nil
}
