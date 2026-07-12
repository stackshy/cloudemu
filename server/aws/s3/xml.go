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

// initiateMultipartUploadResult is the XML response for CreateMultipartUpload.
type initiateMultipartUploadResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	Xmlns    string   `xml:"xmlns,attr"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadID string   `xml:"UploadId"`
}

// completeMultipartUpload is the XML request body for CompleteMultipartUpload.
type completeMultipartUpload struct {
	XMLName xml.Name          `xml:"CompleteMultipartUpload"`
	Parts   []completePartXML `xml:"Part"`
}

type completePartXML struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

// completeMultipartUploadResult is the XML response for CompleteMultipartUpload.
type completeMultipartUploadResult struct {
	XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
	Xmlns    string   `xml:"xmlns,attr"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag"`
}

// listPartsResult is the XML response for ListParts.
type listPartsResult struct {
	XMLName     xml.Name  `xml:"ListPartsResult"`
	Xmlns       string    `xml:"xmlns,attr"`
	Bucket      string    `xml:"Bucket"`
	Key         string    `xml:"Key"`
	UploadID    string    `xml:"UploadId"`
	IsTruncated bool      `xml:"IsTruncated"`
	Parts       []partXML `xml:"Part"`
}

type partXML struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
	Size       int64  `xml:"Size"`
}

// listMultipartUploadsResult is the XML response for ListMultipartUploads.
type listMultipartUploadsResult struct {
	XMLName     xml.Name             `xml:"ListMultipartUploadsResult"`
	Xmlns       string               `xml:"xmlns,attr"`
	Bucket      string               `xml:"Bucket"`
	IsTruncated bool                 `xml:"IsTruncated"`
	Uploads     []multipartUploadXML `xml:"Upload"`
}

type multipartUploadXML struct {
	Key       string `xml:"Key"`
	UploadID  string `xml:"UploadId"`
	Initiated string `xml:"Initiated,omitempty"`
}

// tagging is the XML request/response body for object tagging.
type tagging struct {
	XMLName xml.Name `xml:"Tagging"`
	Xmlns   string   `xml:"xmlns,attr,omitempty"`
	TagSet  []tagXML `xml:"TagSet>Tag"`
}

type tagXML struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

// listVersionsResult is the XML response for ListObjectVersions.
type listVersionsResult struct {
	XMLName        xml.Name           `xml:"ListVersionsResult"`
	Xmlns          string             `xml:"xmlns,attr"`
	Name           string             `xml:"Name"`
	Prefix         string             `xml:"Prefix"`
	Delimiter      string             `xml:"Delimiter,omitempty"`
	MaxKeys        int                `xml:"MaxKeys"`
	IsTruncated    bool               `xml:"IsTruncated"`
	Versions       []objectVersionXML `xml:"Version"`
	CommonPrefixes []prefixXML        `xml:"CommonPrefixes,omitempty"`
}

type objectVersionXML struct {
	Key          string `xml:"Key"`
	VersionID    string `xml:"VersionId"`
	IsLatest     bool   `xml:"IsLatest"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

// versioningConfiguration is the XML request/response body for bucket versioning.
type versioningConfiguration struct {
	XMLName xml.Name `xml:"VersioningConfiguration"`
	Xmlns   string   `xml:"xmlns,attr,omitempty"`
	Status  string   `xml:"Status,omitempty"`
}
