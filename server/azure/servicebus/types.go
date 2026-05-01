package servicebus

// namespaceResource is the ARM JSON shape for Microsoft.ServiceBus/namespaces.
type namespaceResource struct {
	ID         string              `json:"id"`
	Name       string              `json:"name"`
	Type       string              `json:"type"`
	Location   string              `json:"location"`
	Properties namespaceProperties `json:"properties"`
	SKU        *sbSKU              `json:"sku,omitempty"`
}

type namespaceProperties struct {
	ProvisioningState  string `json:"provisioningState"`
	ServiceBusEndpoint string `json:"serviceBusEndpoint,omitempty"`
}

type sbSKU struct {
	Name string `json:"name,omitempty"`
	Tier string `json:"tier,omitempty"`
}

// queueResource is the ARM JSON shape for Microsoft.ServiceBus/namespaces/queues.
type queueResource struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Properties queueProperties `json:"properties"`
}

type queueProperties struct {
	Status                     string `json:"status"`
	MessageCount               int    `json:"messageCount"`
	MaxSizeInMegabytes         int    `json:"maxSizeInMegabytes,omitempty"`
	RequiresDuplicateDetection bool   `json:"requiresDuplicateDetection,omitempty"`
	RequiresSession            bool   `json:"requiresSession,omitempty"`
	DefaultMessageTimeToLive   string `json:"defaultMessageTimeToLive,omitempty"`
	LockDuration               string `json:"lockDuration,omitempty"`
}

// listResponse is the {value: [...]} envelope ARM uses for collection responses.
type listResponse struct {
	Value []any `json:"value"`
}

// createQueueRequest is what we read from a PUT /queues/{name} body. Most
// fields are accepted-then-ignored (the driver doesn't model Service Bus
// dead-letter / forwarding semantics yet).
type createQueueRequest struct {
	Properties queueProperties `json:"properties"`
}

// createNamespaceRequest is what we read from a PUT /namespaces/{ns} body.
type createNamespaceRequest struct {
	Location string `json:"location"`
	SKU      *sbSKU `json:"sku,omitempty"`
}
