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
