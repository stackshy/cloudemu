package queue

import "encoding/xml"

// Azure Queue Storage XML wire shapes. Child element names match the
// azqueue SDK's generated models exactly; the SDK unmarshals into structs and
// ignores the root element name, so only child names are load-bearing.

// enqueueRequest is the body of POST /{queue}/messages.
type enqueueRequest struct {
	XMLName     xml.Name `xml:"QueueMessage"`
	MessageText string   `xml:"MessageText"`
}

// messagesList is the QueueMessagesList body returned by enqueue and dequeue.
type messagesList struct {
	XMLName  xml.Name     `xml:"QueueMessagesList"`
	Messages []messageXML `xml:"QueueMessage"`
}

// messageXML carries the union of fields the SDK reads for enqueued and
// dequeued messages. Empty fields are omitted so enqueue responses stay lean.
type messageXML struct {
	MessageID       string `xml:"MessageId"`
	InsertionTime   string `xml:"InsertionTime"`
	ExpirationTime  string `xml:"ExpirationTime"`
	PopReceipt      string `xml:"PopReceipt"`
	TimeNextVisible string `xml:"TimeNextVisible"`
	DequeueCount    int64  `xml:"DequeueCount,omitempty"`
	MessageText     string `xml:"MessageText,omitempty"`
}

// peekMessagesList is the QueueMessagesList body returned by Peek Messages. It
// omits PopReceipt and TimeNextVisible (a peek is non-destructive) and always
// renders DequeueCount, matching the real service.
type peekMessagesList struct {
	XMLName  xml.Name         `xml:"QueueMessagesList"`
	Messages []peekMessageXML `xml:"QueueMessage"`
}

type peekMessageXML struct {
	MessageID      string `xml:"MessageId"`
	InsertionTime  string `xml:"InsertionTime"`
	ExpirationTime string `xml:"ExpirationTime"`
	DequeueCount   int64  `xml:"DequeueCount"`
	MessageText    string `xml:"MessageText"`
}

// listQueuesResult is the EnumerationResults body for GET /?comp=list.
type listQueuesResult struct {
	XMLName    xml.Name   `xml:"EnumerationResults"`
	Prefix     string     `xml:"Prefix,omitempty"`
	Marker     string     `xml:"Marker,omitempty"`
	MaxResults int        `xml:"MaxResults,omitempty"`
	Queues     queuesList `xml:"Queues"`
	NextMarker string     `xml:"NextMarker"`
}

type queuesList struct {
	Queues []queueXML `xml:"Queue"`
}

type queueXML struct {
	Name string `xml:"Name"`
}

// errorXML is the Azure Storage error envelope.
type errorXML struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

// queryParamRangeErrorXML is the Azure Storage error envelope for an
// out-of-range query parameter. It extends the base error with the offending
// parameter's name and value plus the permitted bounds, matching the body real
// Queue Storage returns for OutOfRangeQueryParameterValue.
type queryParamRangeErrorXML struct {
	XMLName             xml.Name `xml:"Error"`
	Code                string   `xml:"Code"`
	Message             string   `xml:"Message"`
	QueryParameterName  string   `xml:"QueryParameterName"`
	QueryParameterValue string   `xml:"QueryParameterValue"`
	MinimumAllowed      string   `xml:"MinimumAllowed"`
	MaximumAllowed      string   `xml:"MaximumAllowed"`
}
