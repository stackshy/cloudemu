package ecr

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/awsevents"
)

// EventBridge identity of ECR's native image push/delete event. See "Amazon
// ECR events and EventBridge" in the ECR user guide.
const (
	eventSource      = "aws.ecr"
	eventImageAction = "ECR Image Action"
	actionPush       = "PUSH"
	actionDelete     = "DELETE"
	resultSuccess    = "SUCCESS"
)

// imageActionDetail is the detail payload of an "ECR Image Action" event.
type imageActionDetail struct {
	Result            string `json:"result"`
	RepositoryName    string `json:"repository-name"`
	ImageDigest       string `json:"image-digest"`
	ActionType        string `json:"action-type"`
	ImageTag          string `json:"image-tag,omitempty"`
	ManifestMediaType string `json:"manifest-media-type,omitempty"`
}

// SetEventPublisher wires the EventBridge default bus that image push/delete
// actions are published to. Safe to leave unset. No events are emitted.
func (m *Mock) SetEventPublisher(p awsevents.Publisher) {
	m.events.SetPublisher(p)
}

// emitImageAction publishes an "ECR Image Action" event. The real event
// carries an empty resources list. It must be called without m.mu held.
func (m *Mock) emitImageAction(ctx context.Context, action, repository, digest, tag, mediaType string) {
	m.events.Emit(ctx, eventSource, eventImageAction, imageActionDetail{
		Result: resultSuccess, RepositoryName: repository, ImageDigest: digest,
		ActionType: action, ImageTag: tag, ManifestMediaType: mediaType,
	})
}
