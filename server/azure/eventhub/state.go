package eventhub

import (
	"crypto/rand"
	"encoding/base64"
	"time"
)

// In-memory control-plane model for Event Hubs. Namespaces, event hubs,
// consumer groups and authorization rules are Azure-only ARM containers with no
// cross-cloud portable-driver equivalent, so their state lives here on the
// handler, scoped to the parent namespace. Event Hubs has no ARM-reachable data
// plane: sending and receiving events is AMQP/Kafka only, so this handler is
// control-plane only (the same boundary as Service Bus).

const (
	// sasKeyBytes is the length of a generated SAS key before base64 encoding.
	sasKeyBytes = 32
	// defaultRootRuleName is the SAS rule every namespace gets by default.
	defaultRootRuleName = "RootManageSharedAccessKey"
	// defaultConsumerGroup is the built-in consumer group every event hub gets.
	defaultConsumerGroup = "$Default"
	// defaultPartitionCount is Event Hubs' default partition count when a create
	// request omits partitionCount.
	defaultPartitionCount = 4
	// defaultRetentionDays is Event Hubs' default message retention (Standard tier
	// tops out at 7 days) reported when a create request omits it.
	defaultRetentionDays = 7
	// listPageSize is how many entities a list returns before emitting a nextLink.
	listPageSize = 100
	// skipParam is the query parameter a paged list request carries to resume at
	// an offset, mirroring the $skip nextLink real Azure returns.
	skipParam = "$skip"
)

const (
	statusActive = "Active"
	// ehHost is the DNS suffix an Event Hubs namespace maps to; the data plane is
	// AMQP/Kafka against <namespace>.servicebus.windows.net.
	ehHost = ".servicebus.windows.net"
)

type namespaceState struct {
	Name          string
	Location      string
	Subscription  string
	ResourceGroup string
	Tags          map[string]string
	SKU           ehSKU
	Properties    namespaceProperties
	CreatedAt     time.Time
	UpdatedAt     time.Time
	EventHubs     map[string]*eventHubRecord
	AuthRules     map[string]*authRuleRecord
}

type eventHubRecord struct {
	Name           string
	Props          eventHubProperties
	ConsumerGroups map[string]*consumerGroupRecord
	AuthRules      map[string]*authRuleRecord
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type consumerGroupRecord struct {
	Name      string
	Props     consumerGroupProperties
	CreatedAt time.Time
	UpdatedAt time.Time
}

type authRuleRecord struct {
	Name         string
	Rights       []string
	PrimaryKey   string
	SecondaryKey string
}

// generateKey returns a base64-encoded random SAS key.
func generateKey() string {
	b := make([]byte, sasKeyBytes)
	_, _ = rand.Read(b)

	return base64.StdEncoding.EncodeToString(b)
}

func newRootAuthRule() *authRuleRecord {
	return &authRuleRecord{
		Name:         defaultRootRuleName,
		Rights:       []string{"Listen", "Send", "Manage"},
		PrimaryKey:   generateKey(),
		SecondaryKey: generateKey(),
	}
}
