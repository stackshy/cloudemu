package sns

import (
	"net/http"
	"sort"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
)

// baseSubAttributeCount is the number of always-present subscription attributes
// emitted before any caller-set attributes.
const baseSubAttributeCount = 8

// extras returns the AWS-only SNS surface, or writes NotSupported and returns
// false if the wired driver doesn't implement it.
func (h *Handler) extras(w http.ResponseWriter) (snsExtras, bool) {
	ex, ok := h.notif.(snsExtras)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "operation not supported"))
		return nil, false
	}

	return ex, true
}

func (h *Handler) confirmSubscription(w http.ResponseWriter, r *http.Request) {
	ex, ok := h.extras(w)
	if !ok {
		return
	}

	sub, err := ex.ConfirmSubscription(r.Context(),
		topicNameFromARN(r.Form.Get("TopicArn")), r.Form.Get("Token"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, confirmSubscriptionResponse{
		Xmlns:    Namespace,
		Result:   confirmSubscriptionResult{SubscriptionArn: sub.ID},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) getSubscriptionAttributes(w http.ResponseWriter, r *http.Request) {
	ex, ok := h.extras(w)
	if !ok {
		return
	}

	sub, err := ex.GetSubscription(r.Context(), r.Form.Get("SubscriptionArn"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, getSubscriptionAttributesResponse{
		Xmlns: Namespace,
		Result: getSubscriptionAttributesResult{
			Attributes: attributesMap{Entries: subscriptionAttributeEntries(sub)},
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// subscriptionAttributeEntries assembles the SNS attribute map a subscription
// exposes, mirroring GetSubscriptionAttributes.
func subscriptionAttributeEntries(sub *notifdriver.SubscriptionInfo) []attributeEntry {
	pending := attrFalse
	if sub.Status == statusPending {
		pending = attrTrue
	}

	entries := make([]attributeEntry, 0, baseSubAttributeCount+len(sub.Attributes))
	entries = append(entries,
		attributeEntry{Key: "SubscriptionArn", Value: sub.ID},
		attributeEntry{Key: "TopicArn", Value: idgenTopicARN(sub)},
		attributeEntry{Key: "Protocol", Value: sub.Protocol},
		attributeEntry{Key: "Endpoint", Value: sub.Endpoint},
		attributeEntry{Key: "Owner", Value: accountFromARN(sub.ID)},
		attributeEntry{Key: "PendingConfirmation", Value: pending},
		attributeEntry{Key: "ConfirmationWasAuthenticated", Value: attrFalse},
		attributeEntry{Key: "RawMessageDelivery", Value: rawMessageDelivery(sub)},
	)

	// Emit caller-set attributes (FilterPolicy, RedrivePolicy, ...) in a stable
	// order so successive reads don't churn.
	keys := make([]string, 0, len(sub.Attributes))

	for k := range sub.Attributes {
		if k == "RawMessageDelivery" {
			continue // already surfaced above
		}

		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {
		entries = append(entries, attributeEntry{Key: k, Value: sub.Attributes[k]})
	}

	return entries
}

// idgenTopicARN reconstructs the topic ARN from the subscription ARN, which SNS
// forms as <topic-arn>:<sub-uuid>.
func idgenTopicARN(sub *notifdriver.SubscriptionInfo) string {
	arn := sub.ID
	if i := lastColon(arn); i >= 0 {
		return arn[:i]
	}

	return arn
}

func lastColon(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return i
		}
	}

	return -1
}

func rawMessageDelivery(sub *notifdriver.SubscriptionInfo) string {
	if v, ok := sub.Attributes["RawMessageDelivery"]; ok && v == attrTrue {
		return attrTrue
	}

	return attrFalse
}

func (h *Handler) setSubscriptionAttributes(w http.ResponseWriter, r *http.Request) {
	ex, ok := h.extras(w)
	if !ok {
		return
	}

	err := ex.SetSubscriptionAttribute(r.Context(), r.Form.Get("SubscriptionArn"),
		r.Form.Get("AttributeName"), r.Form.Get("AttributeValue"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, setSubscriptionAttributesResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) addPermission(w http.ResponseWriter, r *http.Request) {
	ex, ok := h.extras(w)
	if !ok {
		return
	}

	err := ex.AddTopicPermission(r.Context(), topicNameFromARN(r.Form.Get("TopicArn")),
		r.Form.Get("Label"),
		awsquery.ListStrings(r.Form, "AWSAccountId.member"),
		awsquery.ListStrings(r.Form, "ActionName.member"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, addPermissionResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) removePermission(w http.ResponseWriter, r *http.Request) {
	ex, ok := h.extras(w)
	if !ok {
		return
	}

	err := ex.RemoveTopicPermission(r.Context(),
		topicNameFromARN(r.Form.Get("TopicArn")), r.Form.Get("Label"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, removePermissionResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// publishBatch fans PublishBatchRequestEntries out to individual driver
// Publish calls, collecting per-entry success/failure results.
func (h *Handler) publishBatch(w http.ResponseWriter, r *http.Request) {
	arn := r.Form.Get("TopicArn")
	if arn == "" {
		arn = r.Form.Get("TargetArn")
	}

	topicID := topicNameFromARN(arn)

	idx := awsquery.CollectIndices(r.Form, "PublishBatchRequestEntries.member")

	var (
		successes []batchResultEntry
		failures  []batchErrorEntry
	)

	for _, i := range idx {
		base := "PublishBatchRequestEntries.member." + strconv.Itoa(i)
		entryID := r.Form.Get(base + ".Id")

		out, err := h.notif.Publish(r.Context(), notifdriver.PublishInput{
			TopicID:    topicID,
			Subject:    r.Form.Get(base + ".Subject"),
			Message:    r.Form.Get(base + ".Message"),
			Attributes: parseBatchMessageAttributes(r.Form, base),
		})
		if err != nil {
			failures = append(failures, batchErrorEntry{
				ID: entryID, Code: batchErrorCode(err), Message: err.Error(), SenderFault: true,
			})

			continue
		}

		successes = append(successes, batchResultEntry{ID: entryID, MessageID: out.MessageID})
	}

	awsquery.WriteXMLResponse(w, publishBatchResponse{
		Xmlns: Namespace,
		Result: publishBatchResult{
			Successful: successes,
			Failed:     failures,
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func batchErrorCode(err error) string {
	if cerrors.IsNotFound(err) {
		return "NotFound"
	}

	return "InvalidParameter"
}
