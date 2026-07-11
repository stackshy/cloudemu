package sns

import "encoding/xml"

// All SNS query-protocol responses are wrapped in <FooResponse> with a
// <FooResult> child and a trailing <ResponseMetadata>. The structures below
// mirror the AWS-published XML closely enough that aws-sdk-go-v2's SNS
// unmarshalers consume them without complaint.

type responseMetadata struct {
	RequestID string `xml:"RequestId"`
}

// --- CreateTopic ---

type createTopicResult struct {
	TopicArn string `xml:"TopicArn"`
}

type createTopicResponse struct {
	XMLName  xml.Name          `xml:"CreateTopicResponse"`
	Xmlns    string            `xml:"xmlns,attr"`
	Result   createTopicResult `xml:"CreateTopicResult"`
	Metadata responseMetadata  `xml:"ResponseMetadata"`
}

// --- DeleteTopic / Unsubscribe (empty results) ---

type deleteTopicResponse struct {
	XMLName  xml.Name         `xml:"DeleteTopicResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

type unsubscribeResponse struct {
	XMLName  xml.Name         `xml:"UnsubscribeResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

// --- GetTopicAttributes ---

type attributeEntry struct {
	Key   string `xml:"key"`
	Value string `xml:"value"`
}

type attributesMap struct {
	Entries []attributeEntry `xml:"entry"`
}

type getTopicAttributesResult struct {
	Attributes attributesMap `xml:"Attributes"`
}

type getTopicAttributesResponse struct {
	XMLName  xml.Name                 `xml:"GetTopicAttributesResponse"`
	Xmlns    string                   `xml:"xmlns,attr"`
	Result   getTopicAttributesResult `xml:"GetTopicAttributesResult"`
	Metadata responseMetadata         `xml:"ResponseMetadata"`
}

// --- ListTopics ---

type topicMember struct {
	TopicArn string `xml:"TopicArn"`
}

type topicsList struct {
	Members []topicMember `xml:"member"`
}

type listTopicsResult struct {
	Topics topicsList `xml:"Topics"`
}

type listTopicsResponse struct {
	XMLName  xml.Name         `xml:"ListTopicsResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   listTopicsResult `xml:"ListTopicsResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

// --- Subscribe ---

type subscribeResult struct {
	SubscriptionArn string `xml:"SubscriptionArn"`
}

type subscribeResponse struct {
	XMLName  xml.Name         `xml:"SubscribeResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   subscribeResult  `xml:"SubscribeResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

// --- ListSubscriptions / ListSubscriptionsByTopic ---

type subscriptionMember struct {
	SubscriptionArn string `xml:"SubscriptionArn"`
	TopicArn        string `xml:"TopicArn"`
	Protocol        string `xml:"Protocol"`
	Endpoint        string `xml:"Endpoint"`
	Owner           string `xml:"Owner"`
}

type subscriptionsList struct {
	Members []subscriptionMember `xml:"member"`
}

type listSubscriptionsResult struct {
	Subscriptions subscriptionsList `xml:"Subscriptions"`
}

type listSubscriptionsResponse struct {
	XMLName  xml.Name                `xml:"ListSubscriptionsResponse"`
	Xmlns    string                  `xml:"xmlns,attr"`
	Result   listSubscriptionsResult `xml:"ListSubscriptionsResult"`
	Metadata responseMetadata        `xml:"ResponseMetadata"`
}

type listSubscriptionsByTopicResult struct {
	Subscriptions subscriptionsList `xml:"Subscriptions"`
}

type listSubscriptionsByTopicResponse struct {
	XMLName  xml.Name                       `xml:"ListSubscriptionsByTopicResponse"`
	Xmlns    string                         `xml:"xmlns,attr"`
	Result   listSubscriptionsByTopicResult `xml:"ListSubscriptionsByTopicResult"`
	Metadata responseMetadata               `xml:"ResponseMetadata"`
}

// --- Publish ---

type publishResult struct {
	MessageID string `xml:"MessageId"`
}

type publishResponse struct {
	XMLName  xml.Name         `xml:"PublishResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Result   publishResult    `xml:"PublishResult"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}
