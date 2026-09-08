package iothub

import (
	"context"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

const (
	// eventsEndpoint is the fixed built-in endpoint segment consumer groups nest
	// under (.../IotHubs/{hub}/eventHubEndpoints/events/ConsumerGroups/{name}).
	eventsEndpoint = "events"
	// consumerGroupType is the ARM resource type of a consumer group.
	consumerGroupType = ProviderNamespace + "/" + HubType + "/EventHubEndpoints/ConsumerGroups"
	// defaultConsumerGroup is the built-in consumer group seeded at hub create.
	defaultConsumerGroup = "$Default"
)

// ConsumerGroup is a stored event-hub consumer group nested under a hub's
// built-in "events" endpoint. Its etag is minted once at create and stays stable.
type ConsumerGroup struct {
	Subscription  string `json:"subscription"`
	ResourceGroup string `json:"resourceGroup"`
	HubName       string `json:"hubName"`
	Name          string `json:"name"`
	Etag          string `json:"etag"`
}

// ARMID returns the fully-qualified ARM resource id of the consumer group nested
// under its parent hub's events endpoint.
func (c *ConsumerGroup) ARMID() string {
	return idgen.AzureID(c.Subscription, c.ResourceGroup, ProviderNamespace, HubType, c.HubName) +
		"/eventHubEndpoints/" + eventsEndpoint + "/ConsumerGroups/" + c.Name
}

// ARMType returns the consumer group's ARM resource type.
func (*ConsumerGroup) ARMType() string {
	return consumerGroupType
}

// consumerGroupKey is the case-insensitive store key for a consumer group under a
// hub.
func consumerGroupKey(sub, rg, hub, name string) string {
	return hubKey(sub, rg, hub) + "/" + strings.ToLower(name)
}

// seedDefaultConsumerGroup creates the built-in $Default consumer group for a
// freshly created hub. The caller holds the write lock.
func (m *Mock) seedDefaultConsumerGroup(sub, rg, hub string) {
	k := consumerGroupKey(sub, rg, hub, defaultConsumerGroup)
	m.consumerGroups.Set(k, &ConsumerGroup{
		Subscription:  sub,
		ResourceGroup: rg,
		HubName:       hub,
		Name:          defaultConsumerGroup,
		Etag:          idgen.SyntheticGUID("iothub/cg-etag/" + k),
	})
}

// CreateOrUpdateConsumerGroup creates a consumer group under a hub. The parent
// hub must exist — otherwise it returns a NotFound error (the wire layer maps it
// to ParentResourceNotFound). The etag is minted once at create and preserved.
// It returns the stored consumer group and whether it was newly created.
func (m *Mock) CreateOrUpdateConsumerGroup(
	_ context.Context, sub, rg, hub, name string,
) (ConsumerGroup, bool, error) {
	if err := validateConsumerGroup(sub, rg, hub, name); err != nil {
		return ConsumerGroup{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.hubs.Has(hubKey(sub, rg, hub)) {
		return ConsumerGroup{}, false, cerrors.Newf(cerrors.NotFound, "iot hub %q not found", hub)
	}

	k := consumerGroupKey(sub, rg, hub, name)

	existing, existed := m.consumerGroups.Get(k)
	created := !existed

	c := ConsumerGroup{Subscription: sub, ResourceGroup: rg, HubName: hub, Name: name}
	if existed {
		c.Etag = existing.Etag
	} else {
		c.Etag = idgen.SyntheticGUID("iothub/cg-etag/" + k)
	}

	m.consumerGroups.Set(k, &c)

	return c, created, nil
}

// GetConsumerGroup returns the named consumer group, or a NotFound error.
func (m *Mock) GetConsumerGroup(_ context.Context, sub, rg, hub, name string) (ConsumerGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.consumerGroups.Get(consumerGroupKey(sub, rg, hub, name))
	if !ok {
		return ConsumerGroup{}, cerrors.Newf(cerrors.NotFound, "consumer group %q not found", name)
	}

	return *c, nil
}

// DeleteConsumerGroup removes the named consumer group, reporting whether it
// existed.
func (m *Mock) DeleteConsumerGroup(_ context.Context, sub, rg, hub, name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.consumerGroups.Delete(consumerGroupKey(sub, rg, hub, name)), nil
}

// ListConsumerGroups returns every consumer group under a hub, sorted by name.
// The parent hub must exist — otherwise it returns a NotFound error.
func (m *Mock) ListConsumerGroups(_ context.Context, sub, rg, hub string) ([]ConsumerGroup, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.hubs.Has(hubKey(sub, rg, hub)) {
		return nil, cerrors.Newf(cerrors.NotFound, "iot hub %q not found", hub)
	}

	prefix := hubKey(sub, rg, hub) + "/"

	var out []ConsumerGroup

	for k, c := range m.consumerGroups.All() {
		if strings.HasPrefix(k, prefix) {
			out = append(out, *c)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// validateConsumerGroup rejects a consumer group create with missing fields.
func validateConsumerGroup(sub, rg, hub, name string) error {
	switch {
	case sub == "":
		return cerrors.New(cerrors.InvalidArgument, "subscription is required")
	case rg == "":
		return cerrors.New(cerrors.InvalidArgument, "resource group is required")
	case hub == "":
		return cerrors.New(cerrors.InvalidArgument, "hub name is required")
	case name == "":
		return cerrors.New(cerrors.InvalidArgument, "consumer group name is required")
	default:
		return nil
	}
}
