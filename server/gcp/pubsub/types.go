package pubsub

// topic is the GCP Pub/Sub Topic resource shape.
type topic struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
}

// subscription is the GCP Pub/Sub Subscription resource shape.
type subscription struct {
	Name               string            `json:"name"`
	Topic              string            `json:"topic"`
	AckDeadlineSeconds int               `json:"ackDeadlineSeconds,omitempty"`
	Labels             map[string]string `json:"labels,omitempty"`
}

type listTopicsResponse struct {
	Topics []topic `json:"topics"`
}

type listSubscriptionsResponse struct {
	Subscriptions []subscription `json:"subscriptions"`
}

// publishRequest is POST topics/{name}:publish.
type publishRequest struct {
	Messages []pubsubMessage `json:"messages"`
}

type pubsubMessage struct {
	Data        string            `json:"data"` // base64
	Attributes  map[string]string `json:"attributes,omitempty"`
	OrderingKey string            `json:"orderingKey,omitempty"`
	MessageID   string            `json:"messageId,omitempty"`
}

type publishResponse struct {
	MessageIDs []string `json:"messageIds"`
}

// pullRequest is POST subscriptions/{name}:pull.
type pullRequest struct {
	MaxMessages       int  `json:"maxMessages"`
	ReturnImmediately bool `json:"returnImmediately"`
}

type pullResponse struct {
	ReceivedMessages []receivedMessage `json:"receivedMessages"`
}

type receivedMessage struct {
	AckID   string        `json:"ackId"`
	Message pubsubMessage `json:"message"`
}

// acknowledgeRequest is POST subscriptions/{name}:acknowledge.
type acknowledgeRequest struct {
	AckIDs []string `json:"ackIds"`
}
