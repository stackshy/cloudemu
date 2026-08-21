package notifications

import (
	"context"
	"maps"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/notification/driver"
)

// Protocols ONS delivers over.
const (
	ProtocolEmail     = "EMAIL"
	ProtocolSMS       = "SMS"
	ProtocolHTTPS     = "CUSTOM_HTTPS"
	ProtocolSlack     = "SLACK"
	ProtocolPagerDuty = "PAGERDUTY"
	ProtocolFunctions = "ORACLE_FUNCTIONS"
)

// protocolAliases maps the lowercase names portable callers use onto the ONS
// protocol they name. "http" and "https" are both CUSTOM_HTTPS, the one
// webhook protocol ONS has.
var protocolAliases = map[string]string{ //nolint:gochecknoglobals // lookup table
	"EMAIL":            ProtocolEmail,
	"SMS":              ProtocolSMS,
	"HTTP":             ProtocolHTTPS,
	"HTTPS":            ProtocolHTTPS,
	"CUSTOM_HTTPS":     ProtocolHTTPS,
	"SLACK":            ProtocolSlack,
	"PAGERDUTY":        ProtocolPagerDuty,
	"ORACLE_FUNCTIONS": ProtocolFunctions,
}

// BackoffRetryPolicy is the retry schedule a delivery policy applies.
type BackoffRetryPolicy struct {
	MaxRetryDuration int
	PolicyType       string
}

// DeliveryPolicy is an ONS subscription's delivery configuration.
type DeliveryPolicy struct {
	BackoffRetryPolicy *BackoffRetryPolicy
}

// SubscriptionSpec describes an ONS subscription to create.
type SubscriptionSpec struct {
	TopicID       string
	CompartmentID string
	Protocol      string
	Endpoint      string
	Metadata      string
	FreeformTags  map[string]string
}

// SubscriptionPatch carries the mutable fields of a subscription. A nil field
// leaves the stored one alone.
type SubscriptionPatch struct {
	DeliveryPolicy *DeliveryPolicy
	FreeformTags   map[string]string
}

// Subscription is an ONS subscription in full.
type Subscription struct {
	ID             string
	TopicID        string
	CompartmentID  string
	Protocol       string
	Endpoint       string
	Metadata       string
	LifecycleState string
	// CreatedTime is epoch milliseconds, as ONS reports it.
	CreatedTime    int64
	DeliveryPolicy *DeliveryPolicy
	Etag           string
	// ConfirmationToken is mailed to the endpoint by real ONS. The emulator
	// has no channel to deliver it on, so it is readable here instead.
	ConfirmationToken string
	FreeformTags      map[string]string
}

// ConfirmationResult is what ONS returns from ConfirmSubscription. The
// unsubscribe URL is built by the wire layer, which knows its own origin.
type ConfirmationResult struct {
	TopicName      string
	TopicID        string
	Endpoint       string
	SubscriptionID string
	Token          string
	Message        string
}

// Subscribe creates a subscription in the default compartment. It is the
// portable entry point onto CreateSubscription.
func (m *Mock) Subscribe(ctx context.Context, cfg driver.SubscriptionConfig) (*driver.SubscriptionInfo, error) {
	sub, err := m.CreateSubscription(ctx, SubscriptionSpec{
		TopicID:  cfg.TopicID,
		Protocol: cfg.Protocol,
		Endpoint: cfg.Endpoint,
	})
	if err != nil {
		return nil, err
	}

	return subscriptionInfo(sub), nil
}

// CreateSubscription creates an ONS subscription. It starts PENDING: ONS
// delivers nothing to it until the endpoint owner confirms with the token.
//
//nolint:gocritic // hugeParam: spec is the subscription's full definition.
func (m *Mock) CreateSubscription(_ context.Context, spec SubscriptionSpec) (*Subscription, error) {
	protocol, err := normalizeProtocol(spec.Protocol)
	if err != nil {
		return nil, err
	}

	if spec.Endpoint == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "endpoint is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	td, ok := m.topics.Get(spec.TopicID)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "topic %q not found", spec.TopicID)
	}

	compartment := spec.CompartmentID
	if compartment == "" {
		compartment = td.Scope.Compartment
	}

	for _, existing := range m.subs.SortedValues() {
		if existing.TopicID == td.ID && existing.Protocol == protocol && existing.Endpoint == spec.Endpoint {
			return nil, cerrors.Newf(cerrors.AlreadyExists,
				"subscription to %s endpoint %q on topic %s already exists", protocol, spec.Endpoint, td.ID)
		}
	}

	sub := &Subscription{
		ID:                idgen.OCID(typeSubscription, m.opts.Realm, m.opts.OCIRegion()),
		TopicID:           td.ID,
		CompartmentID:     compartment,
		Protocol:          protocol,
		Endpoint:          spec.Endpoint,
		Metadata:          spec.Metadata,
		LifecycleState:    StatePending,
		CreatedTime:       m.opts.Clock.Now().UTC().UnixMilli(),
		Etag:              idgen.GenerateID("etag-"),
		ConfirmationToken: idgen.GenerateID("token-"),
		FreeformTags:      maps.Clone(spec.FreeformTags),
	}

	m.subs.Set(sub.ID, sub)

	return cloneSubscription(sub), nil
}

// GetSubscription returns a subscription by OCID.
func (m *Mock) GetSubscription(_ context.Context, id string) (*Subscription, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sub, ok := m.subs.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "subscription %q not found", id)
	}

	return cloneSubscription(sub), nil
}

// ListSubscriptions lists the subscriptions on a topic, in the portable shape.
func (m *Mock) ListSubscriptions(_ context.Context, topicID string) ([]driver.SubscriptionInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.topics.Has(topicID) {
		return nil, cerrors.Newf(cerrors.NotFound, "topic %q not found", topicID)
	}

	all := m.subs.SortedValues()
	out := make([]driver.SubscriptionInfo, 0, len(all))

	for _, sub := range all {
		if sub.TopicID != topicID {
			continue
		}

		out = append(out, *subscriptionInfo(sub))
	}

	return out, nil
}

// ListOCISubscriptions lists the subscriptions in a compartment, narrowed to
// one topic when topicID is given. ONS lists subscriptions by compartment
// rather than by topic, which the portable ListSubscriptions cannot express.
func (m *Mock) ListOCISubscriptions(_ context.Context, compartmentID, topicID string) ([]Subscription, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := m.subs.SortedValues()
	out := make([]Subscription, 0, len(all))

	for _, sub := range all {
		if compartmentID != "" && sub.CompartmentID != compartmentID {
			continue
		}

		if topicID != "" && sub.TopicID != topicID {
			continue
		}

		out = append(out, *cloneSubscription(sub))
	}

	return out, nil
}

// UpdateSubscription replaces a subscription's delivery policy and tags.
func (m *Mock) UpdateSubscription(_ context.Context, id string, patch SubscriptionPatch) (*Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sub, ok := m.subs.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "subscription %q not found", id)
	}

	if patch.DeliveryPolicy != nil {
		sub.DeliveryPolicy = cloneDeliveryPolicy(patch.DeliveryPolicy)
	}

	if patch.FreeformTags != nil {
		sub.FreeformTags = maps.Clone(patch.FreeformTags)
	}

	sub.Etag = idgen.GenerateID("etag-")

	m.subs.Set(id, sub)

	return cloneSubscription(sub), nil
}

// Unsubscribe deletes a subscription by OCID.
func (m *Mock) Unsubscribe(_ context.Context, subscriptionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.subs.Delete(subscriptionID) {
		return cerrors.Newf(cerrors.NotFound, "subscription %q not found", subscriptionID)
	}

	m.deliveries.Delete(subscriptionID)

	return nil
}

// ConfirmSubscription moves a subscription from PENDING to ACTIVE. Until it
// runs, a publish to the topic delivers nothing to this subscription.
func (m *Mock) ConfirmSubscription(_ context.Context, id, token, protocol string) (*ConfirmationResult, error) {
	if token == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "token is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sub, ok := m.subs.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "subscription %q not found", id)
	}

	if err := checkToken(sub, token, protocol); err != nil {
		return nil, err
	}

	sub.LifecycleState = StateActive
	sub.Etag = idgen.GenerateID("etag-")

	m.subs.Set(id, sub)

	name := ""
	if td, found := m.topics.Get(sub.TopicID); found {
		name = td.Name
	}

	return &ConfirmationResult{
		TopicName:      name,
		TopicID:        sub.TopicID,
		Endpoint:       sub.Endpoint,
		SubscriptionID: sub.ID,
		Token:          sub.ConfirmationToken,
		Message:        "subscription confirmed",
	}, nil
}

// UnsubscribeByToken deletes a subscription through the unsubscribe link ONS
// puts in every delivery, which authenticates with the confirmation token
// rather than with the caller's credentials.
func (m *Mock) UnsubscribeByToken(_ context.Context, id, token, protocol string) error {
	if token == "" {
		return cerrors.New(cerrors.InvalidArgument, "token is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sub, ok := m.subs.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "subscription %q not found", id)
	}

	if err := checkToken(sub, token, protocol); err != nil {
		return err
	}

	m.subs.Delete(id)
	m.deliveries.Delete(id)

	return nil
}

// ResendSubscriptionConfirmation re-issues the confirmation token for a
// subscription still waiting on one.
func (m *Mock) ResendSubscriptionConfirmation(_ context.Context, id string) (*Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sub, ok := m.subs.Get(id)
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "subscription %q not found", id)
	}

	if sub.LifecycleState != StatePending {
		return nil, cerrors.Newf(cerrors.FailedPrecondition,
			"subscription %q is %s, not %s", id, sub.LifecycleState, StatePending)
	}

	sub.ConfirmationToken = idgen.GenerateID("token-")
	sub.Etag = idgen.GenerateID("etag-")

	m.subs.Set(id, sub)

	return cloneSubscription(sub), nil
}

// ChangeSubscriptionCompartment moves a subscription to another compartment.
func (m *Mock) ChangeSubscriptionCompartment(_ context.Context, id, compartmentID string) error {
	if compartmentID == "" {
		return cerrors.New(cerrors.InvalidArgument, "compartmentId is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	sub, ok := m.subs.Get(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "subscription %q not found", id)
	}

	sub.CompartmentID = compartmentID
	sub.Etag = idgen.GenerateID("etag-")

	m.subs.Set(id, sub)

	return nil
}

// checkToken rejects a confirmation token, or a protocol, that does not belong
// to the subscription. The caller holds mu.
func checkToken(sub *Subscription, token, protocol string) error {
	if token != sub.ConfirmationToken {
		return cerrors.Newf(cerrors.InvalidArgument, "token does not match subscription %q", sub.ID)
	}

	if protocol == "" {
		return nil
	}

	want, err := normalizeProtocol(protocol)
	if err != nil {
		return err
	}

	if want != sub.Protocol {
		return cerrors.Newf(cerrors.InvalidArgument,
			"protocol %s does not match subscription %q", want, sub.ID)
	}

	return nil
}

// normalizeProtocol maps a caller's protocol onto the ONS one, rejecting a
// protocol ONS does not deliver over rather than storing it unused.
func normalizeProtocol(protocol string) (string, error) {
	if protocol == "" {
		return "", cerrors.New(cerrors.InvalidArgument, "protocol is required")
	}

	resolved, ok := protocolAliases[strings.ToUpper(protocol)]
	if !ok {
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"protocol %q is not supported by OCI Notifications; want one of EMAIL, SMS, CUSTOM_HTTPS, "+
				"SLACK, PAGERDUTY, ORACLE_FUNCTIONS", protocol)
	}

	return resolved, nil
}

// subscriptionInfo projects an ONS subscription onto the portable shape.
func subscriptionInfo(sub *Subscription) *driver.SubscriptionInfo {
	status := StatusPending
	if sub.LifecycleState == StateActive {
		status = StatusConfirmed
	}

	return &driver.SubscriptionInfo{
		ID:       sub.ID,
		TopicID:  sub.TopicID,
		Protocol: sub.Protocol,
		Endpoint: sub.Endpoint,
		Status:   status,
	}
}

func cloneSubscription(sub *Subscription) *Subscription {
	out := *sub
	out.FreeformTags = maps.Clone(sub.FreeformTags)
	out.DeliveryPolicy = cloneDeliveryPolicy(sub.DeliveryPolicy)

	return &out
}

func cloneDeliveryPolicy(p *DeliveryPolicy) *DeliveryPolicy {
	if p == nil {
		return nil
	}

	out := DeliveryPolicy{}

	if p.BackoffRetryPolicy != nil {
		retry := *p.BackoffRetryPolicy
		out.BackoffRetryPolicy = &retry
	}

	return &out
}
