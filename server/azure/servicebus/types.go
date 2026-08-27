package servicebus

import "time"

// namespaceResource is the ARM JSON shape for Microsoft.ServiceBus/namespaces.
type namespaceResource struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	Type       string              `json:"type"`
	Location   string              `json:"location"`
	Tags       map[string]string   `json:"tags,omitempty"`
	SKU        *sbSKU              `json:"sku,omitempty"`
	SystemData *systemData         `json:"systemData,omitempty"`
	Properties namespaceProperties `json:"properties"`
}

type namespaceProperties struct {
	ProvisioningState  string     `json:"provisioningState"`
	Status             string     `json:"status,omitempty"`
	ServiceBusEndpoint string     `json:"serviceBusEndpoint,omitempty"`
	MetricID           string     `json:"metricId,omitempty"`
	CreatedAt          *time.Time `json:"createdAt,omitempty"`
	UpdatedAt          *time.Time `json:"updatedAt,omitempty"`
}

type sbSKU struct {
	Name     string `json:"name,omitempty"`
	Tier     string `json:"tier,omitempty"`
	Capacity *int   `json:"capacity,omitempty"`
}

type systemData struct {
	CreatedAt      *time.Time `json:"createdAt,omitempty"`
	CreatedByType  string     `json:"createdByType,omitempty"`
	LastModifiedAt *time.Time `json:"lastModifiedAt,omitempty"`
}

// queueResource is the ARM JSON shape for Microsoft.ServiceBus/namespaces/queues.
type queueResource struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Properties queueProperties `json:"properties"`
}

type queueProperties struct {
	Status                              string        `json:"status,omitempty"`
	CountDetails                        *countDetails `json:"countDetails,omitempty"`
	MessageCount                        int64         `json:"messageCount"`
	SizeInBytes                         int64         `json:"sizeInBytes"`
	MaxSizeInMegabytes                  int32         `json:"maxSizeInMegabytes,omitempty"`
	MaxDeliveryCount                    int32         `json:"maxDeliveryCount,omitempty"`
	LockDuration                        string        `json:"lockDuration,omitempty"`
	DefaultMessageTimeToLive            string        `json:"defaultMessageTimeToLive,omitempty"`
	AutoDeleteOnIdle                    string        `json:"autoDeleteOnIdle,omitempty"`
	DuplicateDetectionHistoryTimeWindow string        `json:"duplicateDetectionHistoryTimeWindow,omitempty"`
	ForwardTo                           string        `json:"forwardTo,omitempty"`
	RequiresDuplicateDetection          bool          `json:"requiresDuplicateDetection"`
	RequiresSession                     bool          `json:"requiresSession"`
	DeadLetteringOnExpiration           bool          `json:"deadLetteringOnMessageExpiration"`
	EnablePartitioning                  bool          `json:"enablePartitioning"`
	EnableExpress                       bool          `json:"enableExpress"`
	EnableBatchedOperations             *bool         `json:"enableBatchedOperations,omitempty"`
	CreatedAt                           *time.Time    `json:"createdAt,omitempty"`
	UpdatedAt                           *time.Time    `json:"updatedAt,omitempty"`
	AccessedAt                          *time.Time    `json:"accessedAt,omitempty"`
}

type countDetails struct {
	ActiveMessageCount             int64 `json:"activeMessageCount"`
	DeadLetterMessageCount         int64 `json:"deadLetterMessageCount"`
	ScheduledMessageCount          int64 `json:"scheduledMessageCount"`
	TransferMessageCount           int64 `json:"transferMessageCount"`
	TransferDeadLetterMessageCount int64 `json:"transferDeadLetterMessageCount"`
}

// topicResource is the ARM JSON shape for .../namespaces/topics.
type topicResource struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Properties topicProperties `json:"properties"`
}

type topicProperties struct {
	Status                              string        `json:"status,omitempty"`
	CountDetails                        *countDetails `json:"countDetails,omitempty"`
	SubscriptionCount                   int32         `json:"subscriptionCount"`
	SizeInBytes                         int64         `json:"sizeInBytes"`
	MaxSizeInMegabytes                  int32         `json:"maxSizeInMegabytes,omitempty"`
	DefaultMessageTimeToLive            string        `json:"defaultMessageTimeToLive,omitempty"`
	AutoDeleteOnIdle                    string        `json:"autoDeleteOnIdle,omitempty"`
	DuplicateDetectionHistoryTimeWindow string        `json:"duplicateDetectionHistoryTimeWindow,omitempty"`
	RequiresDuplicateDetection          bool          `json:"requiresDuplicateDetection"`
	EnablePartitioning                  bool          `json:"enablePartitioning"`
	EnableExpress                       bool          `json:"enableExpress"`
	EnableBatchedOperations             *bool         `json:"enableBatchedOperations,omitempty"`
	SupportOrdering                     bool          `json:"supportOrdering"`
	CreatedAt                           *time.Time    `json:"createdAt,omitempty"`
	UpdatedAt                           *time.Time    `json:"updatedAt,omitempty"`
	AccessedAt                          *time.Time    `json:"accessedAt,omitempty"`
}

// subscriptionResource is the ARM JSON shape for .../topics/subscriptions.
type subscriptionResource struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	Type       string                 `json:"type"`
	Properties subscriptionProperties `json:"properties"`
}

type subscriptionProperties struct {
	Status                     string        `json:"status,omitempty"`
	CountDetails               *countDetails `json:"countDetails,omitempty"`
	MessageCount               int64         `json:"messageCount"`
	MaxDeliveryCount           int32         `json:"maxDeliveryCount,omitempty"`
	LockDuration               string        `json:"lockDuration,omitempty"`
	DefaultMessageTimeToLive   string        `json:"defaultMessageTimeToLive,omitempty"`
	RequiresSession            bool          `json:"requiresSession"`
	DeadLetteringOnExpiration  bool          `json:"deadLetteringOnMessageExpiration"`
	DeadLetteringOnFilterError bool          `json:"deadLetteringOnFilterEvaluationExceptions"`
	ForwardTo                  string        `json:"forwardTo,omitempty"`
	ForwardDeadLetteredTo      string        `json:"forwardDeadLetteredMessagesTo,omitempty"`
	CreatedAt                  *time.Time    `json:"createdAt,omitempty"`
	UpdatedAt                  *time.Time    `json:"updatedAt,omitempty"`
	AccessedAt                 *time.Time    `json:"accessedAt,omitempty"`
}

// ruleResource is the ARM JSON shape for .../subscriptions/rules.
type ruleResource struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Properties ruleProperties `json:"properties"`
}

type ruleProperties struct {
	Action            *ruleAction        `json:"action,omitempty"`
	FilterType        string             `json:"filterType,omitempty"`
	SQLFilter         *sqlFilter         `json:"sqlFilter,omitempty"`
	CorrelationFilter *correlationFilter `json:"correlationFilter,omitempty"`
}

type ruleAction struct {
	SQLExpression      string `json:"sqlExpression,omitempty"`
	CompatibilityLevel int    `json:"compatibilityLevel,omitempty"`
	RequiresPreprocess bool   `json:"requiresPreprocessing,omitempty"`
}

type sqlFilter struct {
	SQLExpression      string `json:"sqlExpression,omitempty"`
	CompatibilityLevel int    `json:"compatibilityLevel,omitempty"`
	RequiresPreprocess bool   `json:"requiresPreprocessing,omitempty"`
}

type correlationFilter struct {
	CorrelationID    string            `json:"correlationId,omitempty"`
	MessageID        string            `json:"messageId,omitempty"`
	To               string            `json:"to,omitempty"`
	ReplyTo          string            `json:"replyTo,omitempty"`
	Label            string            `json:"label,omitempty"`
	SessionID        string            `json:"sessionId,omitempty"`
	ReplyToSessionID string            `json:"replyToSessionId,omitempty"`
	ContentType      string            `json:"contentType,omitempty"`
	Properties       map[string]string `json:"properties,omitempty"`
}

// authRuleResource is the ARM JSON shape for .../namespaces/authorizationRules.
type authRuleResource struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Properties authRuleProperties `json:"properties"`
}

type authRuleProperties struct {
	Rights []string `json:"rights"`
}

// accessKeys is the ARM JSON shape returned by listKeys / regenerateKeys.
type accessKeys struct {
	PrimaryConnectionString   string `json:"primaryConnectionString"`
	SecondaryConnectionString string `json:"secondaryConnectionString"`
	PrimaryKey                string `json:"primaryKey"`
	SecondaryKey              string `json:"secondaryKey"`
	KeyName                   string `json:"keyName"`
}

// checkNameResult is the ARM JSON shape for CheckNameAvailability.
type checkNameResult struct {
	NameAvailable bool   `json:"nameAvailable"`
	Reason        string `json:"reason"`
	Message       string `json:"message"`
}

// listResponse is the {value: [...]} envelope ARM uses for collection responses.
type listResponse struct {
	Value    []any  `json:"value"`
	NextLink string `json:"nextLink,omitempty"`
}

// Request bodies decoded from PUT/POST payloads.

type createNamespaceRequest struct {
	Location string            `json:"location"`
	Tags     map[string]string `json:"tags,omitempty"`
	SKU      *sbSKU            `json:"sku,omitempty"`
}

type updateNamespaceRequest struct {
	Location string            `json:"location,omitempty"`
	Tags     map[string]string `json:"tags,omitempty"`
	SKU      *sbSKU            `json:"sku,omitempty"`
}

type createQueueRequest struct {
	Properties queueProperties `json:"properties"`
}

type createTopicRequest struct {
	Properties topicProperties `json:"properties"`
}

type createSubscriptionRequest struct {
	Properties subscriptionProperties `json:"properties"`
}

type createRuleRequest struct {
	Properties ruleProperties `json:"properties"`
}

type createAuthRuleRequest struct {
	Properties authRuleProperties `json:"properties"`
}

type regenerateKeysRequest struct {
	KeyType string `json:"keyType"`
	Key     string `json:"key,omitempty"`
}

type checkNameRequest struct {
	Name string `json:"name"`
}
