package sns

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	notifdriver "github.com/stackshy/cloudemu/v2/services/notification/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// listPageSize is the fixed number of items SNS returns per ListTopics /
// ListSubscriptions page; callers walk further with the returned NextToken.
const listPageSize = 100

// pageWindow returns the [start,end) slice bounds for the page requested by
// token, plus the NextToken to fetch the following page ("" when exhausted).
func pageWindow(total int, token string) (start, end int, next string) {
	start = decodePageToken(token)
	if start > total {
		start = total
	}

	end = start + listPageSize
	if end >= total {
		return start, total, ""
	}

	return start, end, encodePageToken(end)
}

func decodePageToken(token string) int {
	if token == "" {
		return 0
	}

	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return 0
	}

	n, err := strconv.Atoi(string(raw))
	if err != nil || n < 0 {
		return 0
	}

	return n
}

func encodePageToken(offset int) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func (h *Handler) tagResource(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.notif.(topicTagger)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	name := topicNameFromARN(r.Form.Get("ResourceArn"))

	if err := tagger.TagTopic(r.Context(), name, awsquery.FlatTags(r.Form, "Tags.member")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, tagResourceResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) untagResource(w http.ResponseWriter, r *http.Request) {
	tagger, ok := h.notif.(topicTagger)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "tagging not supported"))
		return
	}

	name := topicNameFromARN(r.Form.Get("ResourceArn"))

	if err := tagger.UntagTopic(r.Context(), name, awsquery.ListStrings(r.Form, "TagKeys.member")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, untagResourceResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// parseMessageAttributes reads SNS Publish MessageAttributes.entry.N.Name /
// .Value.{DataType,StringValue,BinaryValue} form parameters into typed
// attributes, preserving the DataType (String/Number/Binary).
func parseMessageAttributes(form url.Values) map[string]notifdriver.MessageAttribute {
	return parseMessageAttributesAt(form, "MessageAttributes.entry")
}

// parseBatchMessageAttributes reads a PublishBatch entry's MessageAttributes,
// which are nested under the entry's form prefix.
func parseBatchMessageAttributes(form url.Values, entryPrefix string) map[string]notifdriver.MessageAttribute {
	return parseMessageAttributesAt(form, entryPrefix+".MessageAttributes.entry")
}

func parseMessageAttributesAt(form url.Values, prefix string) map[string]notifdriver.MessageAttribute {
	idx := awsquery.CollectIndices(form, prefix)
	if len(idx) == 0 {
		return nil
	}

	out := make(map[string]notifdriver.MessageAttribute, len(idx))

	for _, i := range idx {
		base := prefix + "." + strconv.Itoa(i)

		name := form.Get(base + ".Name")
		if name == "" {
			continue
		}

		out[name] = messageAttributeFromForm(form, base)
	}

	return out
}

// messageAttributeFromForm reads one MessageAttributeValue (DataType plus the
// string or base64 binary value) from its form prefix.
func messageAttributeFromForm(form url.Values, base string) notifdriver.MessageAttribute {
	value := form.Get(base + ".Value.StringValue")
	if binary := form.Get(base + ".Value.BinaryValue"); binary != "" {
		value = binary
	}

	return notifdriver.MessageAttribute{
		DataType: form.Get(base + ".Value.DataType"),
		Value:    value,
	}
}

// attributeValues projects typed message attributes onto the flat name->value
// map used for filter-policy matching and cross-cloud delivery.
func attributeValues(entries map[string]notifdriver.MessageAttribute) map[string]string {
	if len(entries) == 0 {
		return nil
	}

	out := make(map[string]string, len(entries))
	for k, e := range entries {
		out[k] = e.Value
	}

	return out
}

// createTopic maps CreateTopic to Notification.CreateTopic. SNS CreateTopic is
// idempotent: creating a topic that already exists returns the existing ARN
// rather than an error, so we translate the driver's AlreadyExists into a
// lookup + echo of the existing topic.
func (h *Handler) createTopic(w http.ResponseWriter, r *http.Request) {
	name := r.Form.Get("Name")
	attrs := parseSubscriptionAttributes(r.Form)

	info, err := h.notif.CreateTopic(r.Context(), notifdriver.TopicConfig{
		Name:                      name,
		Tags:                      parseSNSTags(r.Form),
		DisplayName:               attrs["DisplayName"],
		Policy:                    attrs["Policy"],
		DeliveryPolicy:            attrs["DeliveryPolicy"],
		KmsMasterKeyID:            attrs["KmsMasterKeyId"],
		FifoTopic:                 attrs["FifoTopic"] == attrTrue,
		ContentBasedDeduplication: attrs["ContentBasedDeduplication"] == attrTrue,
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

	// DeleteTopic is idempotent in SNS: deleting a topic that does not exist is
	// not an error, so a NotFound from the provider is swallowed as success.
	if err := h.notif.DeleteTopic(r.Context(), name); err != nil && !cerrors.IsNotFound(err) {
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
// setTopicAttributes maps SetTopicAttributes to Notification.UpdateTopic,
// persisting DisplayName and Policy. Other attributes (DeliveryPolicy) are
// accepted and dropped.
func (h *Handler) setTopicAttributes(w http.ResponseWriter, r *http.Request) {
	name := topicNameFromARN(r.Form.Get("TopicArn"))

	cfg := notifdriver.TopicConfig{Name: name}

	switch r.Form.Get("AttributeName") {
	case "DisplayName":
		cfg.DisplayName = r.Form.Get("AttributeValue")
	case "Policy":
		cfg.Policy = r.Form.Get("AttributeValue")
	}

	if cfg.DisplayName != "" || cfg.Policy != "" {
		if _, err := h.notif.UpdateTopic(r.Context(), cfg); err != nil {
			writeErr(w, err)
			return
		}
	}

	awsquery.WriteXMLResponse(w, setTopicAttributesResponse{
		Xmlns: Namespace, Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	name := topicNameFromARN(r.Form.Get("ResourceArn"))

	info, err := h.notif.GetTopic(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	members := make([]tagMember, 0, len(info.Tags))
	for k, v := range info.Tags {
		members = append(members, tagMember{Key: k, Value: v})
	}
	// Stable order: the store is a map, and a caller diffing tags on successive
	// reads should not see phantom churn.
	sort.Slice(members, func(i, j int) bool { return members[i].Key < members[j].Key })

	awsquery.WriteXMLResponse(w, listTagsForResourceResponse{
		Xmlns:    Namespace,
		Result:   listTagsResult{Tags: members},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) getTopicAttributes(w http.ResponseWriter, r *http.Request) {
	name := topicNameFromARN(r.Form.Get("TopicArn"))

	info, err := h.notif.GetTopic(r.Context(), name)
	if err != nil {
		writeErr(w, err)
		return
	}

	owner := accountFromARN(info.ResourceID)

	policy := info.Policy
	if policy == "" {
		// Real SNS seeds a default access policy. The aws_sns_topic resource
		// reads Policy back and parses it as JSON, so an absent value fails
		// with "unexpected end of JSON input".
		policy = defaultTopicPolicy(info.ResourceID, owner)
	}

	entries := []attributeEntry{
		{Key: "TopicArn", Value: info.ResourceID},
		{Key: "SubscriptionsConfirmed", Value: strconv.Itoa(info.SubscriptionsConfirmed)},
		{Key: "SubscriptionsPending", Value: strconv.Itoa(info.SubscriptionsPending)},
		{Key: "SubscriptionsDeleted", Value: strconv.Itoa(info.SubscriptionsDeleted)},
		{Key: "Owner", Value: owner},
		{Key: "Policy", Value: policy},
		{Key: "EffectiveDeliveryPolicy", Value: defaultEffectiveDeliveryPolicy},
	}
	entries = append(entries, optionalTopicAttributes(info)...)

	awsquery.WriteXMLResponse(w, getTopicAttributesResponse{
		Xmlns:    Namespace,
		Result:   getTopicAttributesResult{Attributes: attributesMap{Entries: entries}},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// optionalTopicAttributes returns the GetTopicAttributes entries SNS emits only
// when set: DisplayName, DeliveryPolicy, KmsMasterKeyId, and — for a FIFO topic —
// the FifoTopic / ContentBasedDeduplication flags.
func optionalTopicAttributes(info *notifdriver.TopicInfo) []attributeEntry {
	var entries []attributeEntry

	entries = appendNonEmptyAttr(entries, "DisplayName", info.DisplayName)
	entries = appendNonEmptyAttr(entries, "DeliveryPolicy", info.DeliveryPolicy)
	entries = appendNonEmptyAttr(entries, "KmsMasterKeyId", info.KmsMasterKeyID)

	if info.FifoTopic {
		entries = append(entries,
			attributeEntry{Key: "FifoTopic", Value: attrTrue},
			attributeEntry{
				Key:   "ContentBasedDeduplication",
				Value: strconv.FormatBool(info.ContentBasedDeduplication),
			},
		)
	}

	return entries
}

// appendNonEmptyAttr appends a {key,value} entry only when value is non-empty.
func appendNonEmptyAttr(entries []attributeEntry, key, value string) []attributeEntry {
	if value == "" {
		return entries
	}

	return append(entries, attributeEntry{Key: key, Value: value})
}

func (h *Handler) listTopics(w http.ResponseWriter, r *http.Request) {
	topics, err := h.notif.ListTopics(r.Context(), scope.Scope{})
	if err != nil {
		writeErr(w, err)
		return
	}

	start, end, next := pageWindow(len(topics), r.Form.Get("NextToken"))

	members := make([]topicMember, 0, end-start)
	for i := start; i < end; i++ {
		members = append(members, topicMember{TopicArn: topics[i].ResourceID})
	}

	awsquery.WriteXMLResponse(w, listTopicsResponse{
		Xmlns:    Namespace,
		Result:   listTopicsResult{Topics: topicsList{Members: members}, NextToken: next},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// pendingConfirmationARN is the literal SNS returns from Subscribe for a
// subscription that still needs confirmation (unless ReturnSubscriptionArn=true).
const pendingConfirmationARN = "pending confirmation"

func (h *Handler) subscribe(w http.ResponseWriter, r *http.Request) {
	sub, err := h.notif.Subscribe(r.Context(), notifdriver.SubscriptionConfig{
		TopicID:    topicNameFromARN(r.Form.Get("TopicArn")),
		Protocol:   r.Form.Get("Protocol"),
		Endpoint:   r.Form.Get("Endpoint"),
		Attributes: parseSubscriptionAttributes(r.Form),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	// A subscription awaiting confirmation reports the literal "pending
	// confirmation" ARN unless the caller asked for the real ARN.
	arn := sub.ID
	if sub.Status == statusPending && r.Form.Get("ReturnSubscriptionArn") != attrTrue {
		arn = pendingConfirmationARN
	}

	awsquery.WriteXMLResponse(w, subscribeResponse{
		Xmlns:    Namespace,
		Result:   subscribeResult{SubscriptionArn: arn},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// parseSubscriptionAttributes reads the Attributes.entry.N.key / .value form
// parameters SNS Subscribe/SetSubscriptionAttributes serialize into a flat map.
func parseSubscriptionAttributes(form url.Values) map[string]string {
	idx := awsquery.CollectIndices(form, "Attributes.entry")
	if len(idx) == 0 {
		return nil
	}

	out := make(map[string]string, len(idx))

	for _, i := range idx {
		base := "Attributes.entry." + strconv.Itoa(i)

		key := form.Get(base + ".key")
		if key == "" {
			continue
		}

		out[key] = form.Get(base + ".value")
	}

	return out
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
	topics, err := h.notif.ListTopics(r.Context(), scope.Scope{})
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

	start, end, next := pageWindow(len(members), r.Form.Get("NextToken"))
	members = members[start:end]

	awsquery.WriteXMLResponse(w, listSubscriptionsResponse{
		Xmlns:    Namespace,
		Result:   listSubscriptionsResult{Subscriptions: subscriptionsList{Members: members}, NextToken: next},
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

	// Sort by subscription ARN so NextToken offsets stay stable across pages.
	members := subscriptionMembers(info.ResourceID, subs)
	sort.Slice(members, func(i, j int) bool { return members[i].SubscriptionArn < members[j].SubscriptionArn })

	start, end, next := pageWindow(len(members), r.Form.Get("NextToken"))

	awsquery.WriteXMLResponse(w, listSubscriptionsByTopicResponse{
		Xmlns: Namespace,
		Result: listSubscriptionsByTopicResult{
			Subscriptions: subscriptionsList{Members: members[start:end]},
			NextToken:     next,
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

	attrs := parseMessageAttributes(r.Form)

	out, err := h.notif.Publish(r.Context(), notifdriver.PublishInput{
		TopicID:                topicNameFromARN(arn),
		Subject:                r.Form.Get("Subject"),
		Message:                r.Form.Get("Message"),
		Attributes:             attributeValues(attrs),
		AttributeEntries:       attrs,
		MessageStructure:       r.Form.Get("MessageStructure"),
		MessageGroupID:         r.Form.Get("MessageGroupId"),
		MessageDeduplicationID: r.Form.Get("MessageDeduplicationId"),
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
