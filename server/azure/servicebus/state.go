package servicebus

import (
	"crypto/rand"
	"encoding/base64"
	"math"
	"time"
)

// In-memory control-plane model for Service Bus. Namespaces, topics,
// subscriptions, rules and authorization rules are Azure-only ARM containers
// with no cross-cloud portable-driver equivalent, so their state lives here on
// the handler, scoped to the parent namespace. Actual queue message storage is
// delegated to the shared messagequeue driver, addressed per namespace/queue.

const (
	// sasKeyBytes is the length of a generated SAS key before base64 encoding.
	sasKeyBytes = 32
	// defaultMaxDeliveryCount is Service Bus' default for queues/subscriptions.
	defaultMaxDeliveryCount = 10
	// defaultMaxSizeMB is the default entity size for Standard tier.
	defaultMaxSizeMB = 1024
	// sqlCompatibilityLevel is the fixed compatibility level Azure reports.
	sqlCompatibilityLevel = 20
	// defaultRootRuleName is the SAS rule every namespace gets by default.
	defaultRootRuleName = "RootManageSharedAccessKey"
	// listPageSize is how many entities a list returns before emitting a nextLink.
	listPageSize = 100
)

const (
	defaultLockDuration = "PT1M"
	maxTimeToLive       = "P10675199DT2H48M5.4775807S"
	// defaultDupDetectionISO is Service Bus' default duplicate-detection history
	// time window (10 minutes) reported for queues/topics that omit it.
	defaultDupDetectionISO = "PT10M"

	statusActive          = "Active"
	filterTypeSQL         = "SqlFilter"
	filterTypeCorrelation = "CorrelationFilter"
)

// Segment-count landmarks for the topics/subscriptions/rules subtree, counted
// from the first segment after the namespace name.
const (
	depthTopics   = 1
	depthTopic    = 2
	depthSubColl  = 3
	depthSub      = 4
	depthRuleColl = 5
	depthRule     = 6
)

// Segment-count landmarks for the authorizationRules subtree.
const (
	authRuleColl   = 1
	authRuleItem   = 2
	authRuleAction = 3
)

type namespaceState struct {
	Name          string
	Location      string
	Subscription  string
	ResourceGroup string
	Tags          map[string]string
	SKU           sbSKU
	CreatedAt     time.Time
	UpdatedAt     time.Time
	Queues        map[string]*queueRecord
	Topics        map[string]*topicRecord
	AuthRules     map[string]*authRuleRecord
}

type queueRecord struct {
	Name      string
	DriverURL string
	// DLQURL is the backing store of this queue's $DeadLetterQueue sub-queue.
	DLQURL    string
	Props     queueProperties
	CreatedAt time.Time
	UpdatedAt time.Time
}

type topicRecord struct {
	Name      string
	Props     topicProperties
	Subs      map[string]*subscriptionRecord
	CreatedAt time.Time
	UpdatedAt time.Time
}

type subscriptionRecord struct {
	Name      string
	DriverURL string
	// DLQURL is the backing store of this subscription's $DeadLetterQueue.
	DLQURL    string
	Props     subscriptionProperties
	Rules     map[string]*ruleRecord
	CreatedAt time.Time
	UpdatedAt time.Time
}

type ruleRecord struct {
	Name  string
	Props ruleProperties
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

// int32Count converts a non-negative count to int32, saturating at MaxInt32.
func int32Count(n int) int32 {
	if n < 0 {
		return 0
	}

	if n > math.MaxInt32 {
		return math.MaxInt32
	}

	return int32(n)
}
