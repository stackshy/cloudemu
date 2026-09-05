package notifications

// OCI Notifications REST shapes.

// definedTags is OCI's namespaced tag map. CloudEmu does not model tag
// namespaces, so it is echoed back empty and refused on the way in.
type definedTags map[string]map[string]any

type createTopicRequest struct {
	Name          string            `json:"name"`
	CompartmentID string            `json:"compartmentId"`
	Description   string            `json:"description,omitempty"`
	FreeformTags  map[string]string `json:"freeformTags,omitempty"`
	DefinedTags   definedTags       `json:"definedTags,omitempty"`
}

type updateTopicRequest struct {
	Description  string            `json:"description,omitempty"`
	FreeformTags map[string]string `json:"freeformTags,omitempty"`
	DefinedTags  definedTags       `json:"definedTags,omitempty"`
}

// topicResponse is ONS's NotificationTopic. NotificationTopicSummary carries
// the same fields, so lists render it too.
type topicResponse struct {
	TopicID        string            `json:"topicId"`
	Name           string            `json:"name"`
	CompartmentID  string            `json:"compartmentId"`
	APIEndpoint    string            `json:"apiEndpoint"`
	LifecycleState string            `json:"lifecycleState"`
	Description    string            `json:"description,omitempty"`
	TimeCreated    string            `json:"timeCreated,omitempty"`
	Etag           string            `json:"etag,omitempty"`
	ShortTopicID   string            `json:"shortTopicId,omitempty"`
	FreeformTags   map[string]string `json:"freeformTags"`
	DefinedTags    definedTags       `json:"definedTags"`
}

type createSubscriptionRequest struct {
	TopicID       string            `json:"topicId"`
	CompartmentID string            `json:"compartmentId"`
	Protocol      string            `json:"protocol"`
	Endpoint      string            `json:"endpoint"`
	Metadata      string            `json:"metadata,omitempty"`
	FreeformTags  map[string]string `json:"freeformTags,omitempty"`
	DefinedTags   definedTags       `json:"definedTags,omitempty"`
}

type updateSubscriptionRequest struct {
	DeliveryPolicy *deliveryPolicy   `json:"deliveryPolicy,omitempty"`
	FreeformTags   map[string]string `json:"freeformTags,omitempty"`
	DefinedTags    definedTags       `json:"definedTags,omitempty"`
}

type backoffRetryPolicy struct {
	MaxRetryDuration int    `json:"maxRetryDuration"`
	PolicyType       string `json:"policyType"`
}

type deliveryPolicy struct {
	BackoffRetryPolicy *backoffRetryPolicy `json:"backoffRetryPolicy,omitempty"`
}

// subscriptionResponse is ONS's Subscription; SubscriptionSummary shares its
// fields.
type subscriptionResponse struct {
	ID             string            `json:"id"`
	TopicID        string            `json:"topicId"`
	CompartmentID  string            `json:"compartmentId"`
	Protocol       string            `json:"protocol"`
	Endpoint       string            `json:"endpoint"`
	LifecycleState string            `json:"lifecycleState"`
	CreatedTime    int64             `json:"createdTime"`
	Metadata       string            `json:"metadata,omitempty"`
	DeliveryPolicy *deliveryPolicy   `json:"deliveryPolicy,omitempty"`
	Etag           string            `json:"etag,omitempty"`
	FreeformTags   map[string]string `json:"freeformTags"`
	DefinedTags    definedTags       `json:"definedTags"`
	// ConfirmationToken is not an ONS field: real ONS mails it to the
	// endpoint, which the emulator cannot do, so a PENDING subscription
	// carries it back to the caller that must confirm it.
	ConfirmationToken string `json:"confirmationToken,omitempty"`
}

type messageDetails struct {
	Title string `json:"title,omitempty"`
	Body  string `json:"body"`
}

type publishResult struct {
	MessageID string `json:"messageId"`
	TimeStamp string `json:"timeStamp"`
}

type confirmationResult struct {
	TopicName      string `json:"topicName"`
	TopicID        string `json:"topicId"`
	Endpoint       string `json:"endpoint"`
	SubscriptionID string `json:"subscriptionId"`
	UnsubscribeURL string `json:"unsubscribeUrl"`
	Message        string `json:"message,omitempty"`
}

type changeCompartmentRequest struct {
	CompartmentID string `json:"compartmentId"`
}
