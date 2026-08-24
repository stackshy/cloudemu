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
	PutBucketNotification(ctx context.Context, bucket string, configs []s3provider.BucketNotification) error
	GetBucketNotification(ctx context.Context, bucket string) ([]s3provider.BucketNotification, error)
}

// filterRuleXML is one S3Key name-filter rule (<Name>prefix|suffix</Name>).
type filterRuleXML struct {
	Name  string `xml:"Name"`
	Value string `xml:"Value"`
}

type s3KeyFilterXML struct {
	FilterRules []filterRuleXML `xml:"FilterRule"`
}

// notificationFilterXML is the <Filter><S3Key>… key-name filter shared by all
// three destination kinds.
type notificationFilterXML struct {
	S3Key s3KeyFilterXML `xml:"S3Key"`
}

// queueConfigurationXML targets an SQS queue (<Queue> = queue ARN).
type queueConfigurationXML struct {
	ID     string                 `xml:"Id,omitempty"`
	Queue  string                 `xml:"Queue"`
	Events []string               `xml:"Event"`
	Filter *notificationFilterXML `xml:"Filter,omitempty"`
}

// topicConfigurationXML targets an SNS topic (<Topic> = topic ARN).
type topicConfigurationXML struct {
	ID     string                 `xml:"Id,omitempty"`
	Topic  string                 `xml:"Topic"`
	Events []string               `xml:"Event"`
	Filter *notificationFilterXML `xml:"Filter,omitempty"`
}

// lambdaConfigurationXML targets a Lambda function. Its wire element is
// <CloudFunctionConfiguration> with the ARN in <CloudFunction> — the names the
// AWS SDKs marshal LambdaFunctionConfiguration to.
type lambdaConfigurationXML struct {
	ID            string                 `xml:"Id,omitempty"`
	CloudFunction string                 `xml:"CloudFunction"`
	Events        []string               `xml:"Event"`
	Filter        *notificationFilterXML `xml:"Filter,omitempty"`
}

type notificationConfigurationXML struct {
	XMLName              xml.Name                 `xml:"NotificationConfiguration"`
	Xmlns                string                   `xml:"xmlns,attr,omitempty"`
	QueueConfigurations  []queueConfigurationXML  `xml:"QueueConfiguration"`
	TopicConfigurations  []topicConfigurationXML  `xml:"TopicConfiguration"`
	LambdaConfigurations []lambdaConfigurationXML `xml:"CloudFunctionConfiguration"`
}

// bucketNotificationOp dispatches PUT/GET for the bucket ?notification
// sub-resource. Without this a PUT ?notification fell through to CreateBucket
// (BucketAlreadyOwnedByYou), and S3 event pipelines could not be wired.
func (h *Handler) bucketNotificationOp(w http.ResponseWriter, r *http.Request, bucket string) {
	notifier, ok := h.bucket.(bucketNotifier)
	if !ok {
		writeError(w, http.StatusNotImplemented, "NotImplemented", "notifications not supported")
		return
	}

	switch r.Method {
	case http.MethodPut:
		h.putBucketNotification(w, r, bucket, notifier)
	case http.MethodGet:
		h.getBucketNotification(w, r, bucket, notifier)
	default:
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "method not allowed")
	}
}

func (*Handler) putBucketNotification(w http.ResponseWriter, r *http.Request, bucket string, notifier bucketNotifier) {
	var body notificationConfigurationXML
	if err := xml.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "MalformedXML", "could not parse request body")
		return
	}

	configs := make([]s3provider.BucketNotification, 0,
		len(body.QueueConfigurations)+len(body.TopicConfigurations)+len(body.LambdaConfigurations))

	for _, qc := range body.QueueConfigurations {
		configs = append(configs, s3provider.BucketNotification{
			ID: qc.ID, Target: s3provider.NotifyQueue, ARN: qc.Queue, Events: qc.Events, Filters: filtersFromXML(qc.Filter),
		})
	}

	for _, tc := range body.TopicConfigurations {
		configs = append(configs, s3provider.BucketNotification{
			ID: tc.ID, Target: s3provider.NotifyTopic, ARN: tc.Topic, Events: tc.Events, Filters: filtersFromXML(tc.Filter),
		})
	}

	for _, lc := range body.LambdaConfigurations {
		configs = append(configs, s3provider.BucketNotification{
			ID: lc.ID, Target: s3provider.NotifyLambda, ARN: lc.CloudFunction, Events: lc.Events, Filters: filtersFromXML(lc.Filter),
		})
	}

	if err := notifier.PutBucketNotification(r.Context(), bucket, configs); err != nil {
		writeErr(w, err)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (*Handler) getBucketNotification(w http.ResponseWriter, r *http.Request, bucket string, notifier bucketNotifier) {
	configs, err := notifier.GetBucketNotification(r.Context(), bucket)
	if err != nil {
		writeErr(w, err)
		return
	}

	resp := notificationConfigurationXML{Xmlns: xmlns}

	for i := range configs {
		c := &configs[i]

		switch c.Target {
		case s3provider.NotifyTopic:
			resp.TopicConfigurations = append(resp.TopicConfigurations, topicConfigurationXML{
				ID: c.ID, Topic: c.ARN, Events: c.Events, Filter: filtersToXML(c.Filters),
			})
		case s3provider.NotifyLambda:
			resp.LambdaConfigurations = append(resp.LambdaConfigurations, lambdaConfigurationXML{
				ID: c.ID, CloudFunction: c.ARN, Events: c.Events, Filter: filtersToXML(c.Filters),
			})
		default:
			resp.QueueConfigurations = append(resp.QueueConfigurations, queueConfigurationXML{
				ID: c.ID, Queue: c.ARN, Events: c.Events, Filter: filtersToXML(c.Filters),
			})
		}
	}

	wire.WriteXML(w, http.StatusOK, resp)
}

// filtersFromXML flattens an optional <Filter><S3Key> element into the provider
// filter-rule list.
func filtersFromXML(f *notificationFilterXML) []s3provider.NotificationFilterRule {
	if f == nil || len(f.S3Key.FilterRules) == 0 {
		return nil
	}

	rules := make([]s3provider.NotificationFilterRule, 0, len(f.S3Key.FilterRules))
	for _, r := range f.S3Key.FilterRules {
		rules = append(rules, s3provider.NotificationFilterRule{Name: r.Name, Value: r.Value})
	}

	return rules
}

// filtersToXML rebuilds the <Filter><S3Key> element from provider filter rules,
// returning nil (so the element is omitted) when there are none.
func filtersToXML(rules []s3provider.NotificationFilterRule) *notificationFilterXML {
	if len(rules) == 0 {
		return nil
	}

	out := &notificationFilterXML{}
	for _, r := range rules {
		out.S3Key.FilterRules = append(out.S3Key.FilterRules, filterRuleXML{Name: r.Name, Value: r.Value})
	}

	return out
}
