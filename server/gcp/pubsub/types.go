package pubsub

import "encoding/json"

// topic is the GCP Pub/Sub Topic resource shape.
type topic struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
}

// subscription is the GCP Pub/Sub Subscription resource shape. Extended fields
// (pushConfig/retryPolicy/deadLetterPolicy/expirationPolicy) are kept as raw
// JSON so they round-trip verbatim without modeling every nested member.
type subscription struct {
	Name                     string            `json:"name"`
	Topic                    string            `json:"topic"`
	PushConfig               json.RawMessage   `json:"pushConfig,omitempty"`
	AckDeadlineSeconds       int               `json:"ackDeadlineSeconds,omitempty"`
	RetainAckedMessages      bool              `json:"retainAckedMessages,omitempty"`
	MessageRetentionDuration string            `json:"messageRetentionDuration,omitempty"`
	Labels                   map[string]string `json:"labels,omitempty"`
	EnableMessageOrdering    bool              `json:"enableMessageOrdering,omitempty"`
	ExpirationPolicy         json.RawMessage   `json:"expirationPolicy,omitempty"`
	Filter                   string            `json:"filter,omitempty"`
	DeadLetterPolicy         json.RawMessage   `json:"deadLetterPolicy,omitempty"`
	RetryPolicy              json.RawMessage   `json:"retryPolicy,omitempty"`
	Detached                 bool              `json:"detached,omitempty"`
}

// updateTopicRequest is PATCH topics/{name} (topics.patch): the topic fields to
// update plus the field mask naming which of them to apply.
type updateTopicRequest struct {
	Topic      topic  `json:"topic"`
	UpdateMask string `json:"updateMask"`
}

// updateSubscriptionRequest is PATCH subscriptions/{name} (subscriptions.patch).
type updateSubscriptionRequest struct {
	Subscription subscription `json:"subscription"`
	UpdateMask   string       `json:"updateMask"`
}

// deadLetterPolicyJSON is the parsed shape of subscription.deadLetterPolicy,
// used to route exhausted messages to the dead-letter topic.
type deadLetterPolicyJSON struct {
	DeadLetterTopic     string `json:"deadLetterTopic"`
	MaxDeliveryAttempts int    `json:"maxDeliveryAttempts"`
}

type listTopicsResponse struct {
	Topics        []topic `json:"topics"`
	NextPageToken string  `json:"nextPageToken,omitempty"`
}

type listSubscriptionsResponse struct {
	Subscriptions []subscription `json:"subscriptions"`
	NextPageToken string         `json:"nextPageToken,omitempty"`
}

// listTopicSubscriptionsResponse is topics.subscriptions.list — a list of
// subscription resource names (strings), not full Subscription objects.
type listTopicSubscriptionsResponse struct {
	Subscriptions []string `json:"subscriptions"`
	NextPageToken string   `json:"nextPageToken,omitempty"`
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
	PublishTime string            `json:"publishTime,omitempty"`
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
	AckID           string        `json:"ackId"`
	Message         pubsubMessage `json:"message"`
	DeliveryAttempt int           `json:"deliveryAttempt,omitempty"`
}

// acknowledgeRequest is POST subscriptions/{name}:acknowledge.
type acknowledgeRequest struct {
	AckIDs []string `json:"ackIds"`
}

// modifyAckDeadlineRequest is POST subscriptions/{name}:modifyAckDeadline.
type modifyAckDeadlineRequest struct {
	AckIDs             []string `json:"ackIds"`
	AckDeadlineSeconds int      `json:"ackDeadlineSeconds"`
}

// modifyPushConfigRequest is POST subscriptions/{name}:modifyPushConfig.
type modifyPushConfigRequest struct {
	PushConfig json.RawMessage `json:"pushConfig"`
}

// seekRequest is POST subscriptions/{name}:seek.
type seekRequest struct {
	Time     string `json:"time,omitempty"`
	Snapshot string `json:"snapshot,omitempty"`
}

// snapshot is the GCP Pub/Sub Snapshot resource shape.
type snapshot struct {
	Name       string            `json:"name"`
	Topic      string            `json:"topic"`
	ExpireTime string            `json:"expireTime,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
}

// createSnapshotRequest is PUT snapshots/{name}.
type createSnapshotRequest struct {
	Subscription string            `json:"subscription"`
	Labels       map[string]string `json:"labels,omitempty"`
}

type listSnapshotsResponse struct {
	Snapshots     []snapshot `json:"snapshots"`
	NextPageToken string     `json:"nextPageToken,omitempty"`
}

// iamPolicy is the shared google.iam.v1.Policy shape.
type iamPolicy struct {
	Version  int          `json:"version,omitempty"`
	Bindings []iamBinding `json:"bindings,omitempty"`
	Etag     string       `json:"etag,omitempty"`
}

type iamBinding struct {
	Role      string          `json:"role"`
	Members   []string        `json:"members"`
	Condition json.RawMessage `json:"condition,omitempty"`
}

type setIamPolicyRequest struct {
	Policy iamPolicy `json:"policy"`
}

type testIamPermissionsRequest struct {
	Permissions []string `json:"permissions"`
}

type testIamPermissionsResponse struct {
	Permissions []string `json:"permissions,omitempty"`
}
