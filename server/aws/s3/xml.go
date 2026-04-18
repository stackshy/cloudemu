package s3

import "encoding/xml"

// errorXML is the XML error response format used by S3.
type errorXML struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

// listAllMyBucketsResult is the XML response for ListBuckets.
type listAllMyBucketsResult struct {
	XMLName xml.Name    `xml:"ListAllMyBucketsResult"`
	Xmlns   string      `xml:"xmlns,attr"`
	Buckets []bucketXML `xml:"Buckets>Bucket"`
}

type bucketXML struct {
	Name         string `xml:"Name"`
	CreationDate string `xml:"CreationDate"`
}

// listBucketResult is the XML response for ListObjectsV2.
type listBucketResult struct {
	XMLName               xml.Name    `xml:"ListBucketResult"`
	Xmlns                 string      `xml:"xmlns,attr"`
	Name                  string      `xml:"Name"`
	Prefix                string      `xml:"Prefix"`
	Delimiter             string      `xml:"Delimiter,omitempty"`
	MaxKeys               int         `xml:"MaxKeys"`
	IsTruncated           bool        `xml:"IsTruncated"`
	Contents              []objectXML `xml:"Contents"`
	CommonPrefixes        []prefixXML `xml:"CommonPrefixes,omitempty"`
	KeyCount              int         `xml:"KeyCount"`
	ContinuationToken     string      `xml:"ContinuationToken,omitempty"`
	NextContinuationToken string      `xml:"NextContinuationToken,omitempty"`
}

type objectXML struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int    `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

type prefixXML struct {
	Prefix string `xml:"Prefix"`
}

// copyObjectResult is the XML response for CopyObject.
type copyObjectResult struct {
	XMLName      xml.Name `xml:"CopyObjectResult"`
	Xmlns        string   `xml:"xmlns,attr"`
	ETag         string   `xml:"ETag"`
	LastModified string   `xml:"LastModified"`
}
