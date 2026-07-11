package sns

import (
	"net/http"
	"net/url"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/errors"
	notifdriver "github.com/stackshy/cloudemu/notification/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

// createTopic maps CreateTopic to Notification.CreateTopic. SNS CreateTopic is
// idempotent: creating a topic that already exists returns the existing ARN
// rather than an error, so we translate the driver's AlreadyExists into a
// lookup + echo of the existing topic.
func (h *Handler) createTopic(w http.ResponseWriter, r *http.Request) {
	name := r.Form.Get("Name")

	info, err := h.notif.CreateTopic(r.Context(), notifdriver.TopicConfig{
		Name: name,
		Tags: parseSNSTags(r.Form),
	})
	if err != nil {
		if cerrors.IsAlreadyExists(err) {
			if existing, gerr := h.notif.GetTopic(r.Context(), name); gerr == nil {
				h.writeCreateTopic(w, existing.ResourceID)
				return
			}
		}

		writeErr(w, err)

		return
	}

	h.writeCreateTopic(w, info.ResourceID)
}

func (h *Handler) writeCreateTopic(w http.ResponseWriter, arn string) {
	awsquery.WriteXMLResponse(w, createTopicResponse{
		Xmlns:    Namespace,
		Result:   createTopicResult{TopicArn: arn},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteTopic(w http.ResponseWriter, r *http.Request) {
	name := topicNameFromARN(r.Form.Get("TopicArn"))

	if err := h.notif.DeleteTopic(r.Context(), name); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteTopicResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// getTopicAttributes maps GetTopicAttributes to Notification.GetTopic and
// exposes the topic's ARN, display name, and subscription count as the standard
// SNS attribute map.
func (h *Handler) getTopicAttributes(w http.ResponseWriter, r *http.Request) {
	name := topicNameFromARN(r.Form.Get("TopicArn"))

	info, err := h.notif.GetTopic(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	entries := []attributeEntry{
		{Key: "TopicArn", Value: info.ResourceID},
		{Key: "SubscriptionsConfirmed", Value: strconv.Itoa(info.SubscriptionCount)},
	}
	if info.DisplayName != "" {
		entries = append(entries, attributeEntry{Key: "DisplayName", Value: info.DisplayName})
	}

	awsquery.WriteXMLResponse(w, getTopicAttributesResponse{
		Xmlns:    Namespace,
		Result:   getTopicAttributesResult{Attributes: attributesMap{Entries: entries}},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listTopics(w http.ResponseWriter, r *http.Request) {
	topics, err := h.notif.ListTopics(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	members := make([]topicMember, 0, len(topics))
	for i := range topics {
		members = append(members, topicMember{TopicArn: topics[i].ResourceID})
	}

	awsquery.WriteXMLResponse(w, listTopicsResponse{
		Xmlns:    Namespace,
		Result:   listTopicsResult{Topics: topicsList{Members: members}},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) subscribe(w http.ResponseWriter, r *http.Request) {
	sub, err := h.notif.Subscribe(r.Context(), notifdriver.SubscriptionConfig{
		TopicID:  topicNameFromARN(r.Form.Get("TopicArn")),
		Protocol: r.Form.Get("Protocol"),
		Endpoint: r.Form.Get("Endpoint"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, subscribeResponse{
		Xmlns:    Namespace,
		Result:   subscribeResult{SubscriptionArn: sub.ID},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) unsubscribe(w http.ResponseWriter, r *http.Request) {
	if err := h.notif.Unsubscribe(r.Context(), r.Form.Get("SubscriptionArn")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, unsubscribeResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// listSubscriptions maps ListSubscriptions to the driver's per-topic
// ListSubscriptions aggregated across every topic, since the driver has no
// global subscription index.
func (h *Handler) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	topics, err := h.notif.ListTopics(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}

	var members []subscriptionMember

	for i := range topics {
		subs, serr := h.notif.ListSubscriptions(r.Context(), topics[i].Name)
		if serr != nil {
			writeErr(w, serr)
			return
		}

		members = append(members, subscriptionMembers(topics[i].ResourceID, subs)...)
	}

	awsquery.WriteXMLResponse(w, listSubscriptionsResponse{
		Xmlns:    Namespace,
		Result:   listSubscriptionsResult{Subscriptions: subscriptionsList{Members: members}},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listSubscriptionsByTopic(w http.ResponseWriter, r *http.Request) {
	name := topicNameFromARN(r.Form.Get("TopicArn"))

	info, err := h.notif.GetTopic(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	subs, err := h.notif.ListSubscriptions(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, listSubscriptionsByTopicResponse{
		Xmlns: Namespace,
		Result: listSubscriptionsByTopicResult{
			Subscriptions: subscriptionsList{Members: subscriptionMembers(info.ResourceID, subs)},
		},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) publish(w http.ResponseWriter, r *http.Request) {
	// SNS accepts either TopicArn or TargetArn to address the destination.
	arn := r.Form.Get("TopicArn")
	if arn == "" {
		arn = r.Form.Get("TargetArn")
	}

	out, err := h.notif.Publish(r.Context(), notifdriver.PublishInput{
		TopicID: topicNameFromARN(arn),
		Subject: r.Form.Get("Subject"),
		Message: r.Form.Get("Message"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, publishResponse{
		Xmlns:    Namespace,
		Result:   publishResult{MessageID: out.MessageID},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// subscriptionMembers converts driver subscriptions into SNS XML members. The
// driver's SubscriptionInfo.ID is already the subscription ARN; TopicArn is the
// topic's resource ARN passed by the caller.
func subscriptionMembers(topicArn string, subs []notifdriver.SubscriptionInfo) []subscriptionMember {
	out := make([]subscriptionMember, 0, len(subs))
	for i := range subs {
		out = append(out, subscriptionMember{
			SubscriptionArn: subs[i].ID,
			TopicArn:        topicArn,
			Protocol:        subs[i].Protocol,
			Endpoint:        subs[i].Endpoint,
		})
	}

	return out
}

// parseSNSTags parses the Tags.member.N.{Key,Value} form entries emitted by the
// SNS SDK's TagList serializer.
func parseSNSTags(form url.Values) map[string]string {
	indices := awsquery.CollectIndices(form, "Tags.member")
	if len(indices) == 0 {
		return nil
	}

	out := make(map[string]string, len(indices))

	for _, n := range indices {
		base := "Tags.member." + strconv.Itoa(n)
		if k := form.Get(base + ".Key"); k != "" {
			out[k] = form.Get(base + ".Value")
		}
	}

	return out
}
