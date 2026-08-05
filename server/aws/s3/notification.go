package s3

import (
	"context"
	"encoding/xml"
	"net/http"

	s3provider "github.com/stackshy/cloudemu/v2/providers/aws/s3"
	"github.com/stackshy/cloudemu/v2/server/wire"
)

// bucketNotifier is the AWS-specific bucket-notification surface. It's not part
// of the portable Bucket driver (Azure Blob / GCS notify differently), so the
// handler type-asserts for it.
type bucketNotifier interface {
	PutBucketNotification(ctx context.Context, bucket string, configs []s3provider.QueueNotification) error
	GetBucketNotification(ctx context.Context, bucket string) ([]s3provider.QueueNotification, error)
}

type queueConfigurationXML struct {
	ID     string   `xml:"Id,omitempty"`
	Queue  string   `xml:"Queue"`
	Events []string `xml:"Event"`
}

type notificationConfigurationXML struct {
	XMLName             xml.Name                `xml:"NotificationConfiguration"`
	Xmlns               string                  `xml:"xmlns,attr,omitempty"`
	QueueConfigurations []queueConfigurationXML `xml:"QueueConfiguration"`
}

// bucketNotificationOp dispatches PUT/GET for the bucket ?notification
// sub-resource. Without this a PUT ?notification fell through to CreateBucket
// (BucketAlreadyOwnedByYou), and S3 -> SQS event pipelines could not be wired.
func (h *Handler) bucketNotificationOp(w http.ResponseWriter, r *http.Request, bucket string) {
	notifier, ok := h.bucket.(bucketNotifier)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "notifications not supported")
		return
	}

	switch r.Method {
	case http.MethodPut:
		var body notificationConfigurationXML
		if err := xml.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "MalformedXML", "could not parse request body")
			return
		}

		configs := make([]s3provider.QueueNotification, 0, len(body.QueueConfigurations))
		for _, qc := range body.QueueConfigurations {
			configs = append(configs, s3provider.QueueNotification{
				ID: qc.ID, QueueARN: qc.Queue, Events: qc.Events,
			})
		}

		if err := notifier.PutBucketNotification(r.Context(), bucket, configs); err != nil {
			writeErr(w, err)
			return
		}

		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		configs, err := notifier.GetBucketNotification(r.Context(), bucket)
		if err != nil {
			writeErr(w, err)
			return
		}

		resp := notificationConfigurationXML{Xmlns: xmlns}
		for _, c := range configs {
			resp.QueueConfigurations = append(resp.QueueConfigurations, queueConfigurationXML{
				ID: c.ID, Queue: c.QueueARN, Events: c.Events,
			})
		}

		wire.WriteXML(w, http.StatusOK, resp)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}
