package objectstorage

// namespaceMetadataBody is the response of GetNamespaceMetadata.
type namespaceMetadataBody struct {
	Namespace                 string `json:"namespace"`
	DefaultS3CompartmentID    string `json:"defaultS3CompartmentId"`
	DefaultSwiftCompartmentID string `json:"defaultSwiftCompartmentId"`
}

// createBucketBody is the CreateBucket request.
type createBucketBody struct {
	Name                string                       `json:"name"`
	CompartmentID       string                       `json:"compartmentId"`
	PublicAccessType    string                       `json:"publicAccessType"`
	StorageTier         string                       `json:"storageTier"`
	Versioning          string                       `json:"versioning"`
	KMSKeyID            string                       `json:"kmsKeyId"`
	AutoTiering         string                       `json:"autoTiering"`
	ObjectEventsEnabled bool                         `json:"objectEventsEnabled"`
	Metadata            map[string]string            `json:"metadata"`
	FreeformTags        map[string]string            `json:"freeformTags"`
	DefinedTags         map[string]map[string]string `json:"definedTags"`
}

// updateBucketBody is the UpdateBucket request. Pointers distinguish a field
// the caller sent from one it omitted, which is what OCI's partial update
// needs.
type updateBucketBody struct {
	CompartmentID       *string                      `json:"compartmentId"`
	PublicAccessType    *string                      `json:"publicAccessType"`
	Versioning          *string                      `json:"versioning"`
	KMSKeyID            *string                      `json:"kmsKeyId"`
	AutoTiering         *string                      `json:"autoTiering"`
	ObjectEventsEnabled *bool                        `json:"objectEventsEnabled"`
	Metadata            map[string]string            `json:"metadata"`
	FreeformTags        map[string]string            `json:"freeformTags"`
	DefinedTags         map[string]map[string]string `json:"definedTags"`
}

// bucketBody is a bucket as OCI reports it.
type bucketBody struct {
	ID                  string                       `json:"id"`
	Namespace           string                       `json:"namespace"`
	Name                string                       `json:"name"`
	CompartmentID       string                       `json:"compartmentId"`
	CreatedBy           string                       `json:"createdBy"`
	TimeCreated         string                       `json:"timeCreated"`
	ETag                string                       `json:"etag"`
	PublicAccessType    string                       `json:"publicAccessType"`
	StorageTier         string                       `json:"storageTier"`
	Versioning          string                       `json:"versioning"`
	KMSKeyID            string                       `json:"kmsKeyId,omitempty"`
	AutoTiering         string                       `json:"autoTiering"`
	ObjectEventsEnabled bool                         `json:"objectEventsEnabled"`
	ReplicationEnabled  bool                         `json:"replicationEnabled"`
	IsReadOnly          bool                         `json:"isReadOnly"`
	Metadata            map[string]string            `json:"metadata,omitempty"`
	FreeformTags        map[string]string            `json:"freeformTags,omitempty"`
	DefinedTags         map[string]map[string]string `json:"definedTags,omitempty"`
	ApproximateCount    int64                        `json:"approximateCount"`
	ApproximateSize     int64                        `json:"approximateSize"`
}

// bucketSummaryBody is one entry of ListBuckets. OCI's summary is deliberately
// thinner than the full bucket.
type bucketSummaryBody struct {
	Namespace     string                       `json:"namespace"`
	Name          string                       `json:"name"`
	CompartmentID string                       `json:"compartmentId"`
	CreatedBy     string                       `json:"createdBy"`
	TimeCreated   string                       `json:"timeCreated"`
	ETag          string                       `json:"etag"`
	FreeformTags  map[string]string            `json:"freeformTags,omitempty"`
	DefinedTags   map[string]map[string]string `json:"definedTags,omitempty"`
}

// objectSummaryBody is one entry of ListObjects.
type objectSummaryBody struct {
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	MD5          string `json:"md5,omitempty"`
	ETag         string `json:"etag,omitempty"`
	TimeCreated  string `json:"timeCreated,omitempty"`
	TimeModified string `json:"timeModified,omitempty"`
	StorageTier  string `json:"storageTier,omitempty"`
}

// listObjectsBody is the ListObjects response.
type listObjectsBody struct {
	Objects       []objectSummaryBody `json:"objects"`
	Prefixes      []string            `json:"prefixes,omitempty"`
	NextStartWith string              `json:"nextStartWith,omitempty"`
}

// objectVersionBody is one entry of ListObjectVersions.
type objectVersionBody struct {
	Name           string `json:"name"`
	Size           int64  `json:"size"`
	ETag           string `json:"etag,omitempty"`
	TimeModified   string `json:"timeModified,omitempty"`
	VersionID      string `json:"versionId"`
	IsDeleteMarker bool   `json:"isDeleteMarker"`
}

// listObjectVersionsBody is the ListObjectVersions response.
type listObjectVersionsBody struct {
	Items    []objectVersionBody `json:"items"`
	Prefixes []string            `json:"prefixes,omitempty"`
}

// renameObjectBody is the renameObject action request.
type renameObjectBody struct {
	SourceName string `json:"sourceName"`
	NewName    string `json:"newName"`
}

// copyObjectBody is the copyObject action request.
type copyObjectBody struct {
	SourceObjectName      string `json:"sourceObjectName"`
	DestinationRegion     string `json:"destinationRegion"`
	DestinationNamespace  string `json:"destinationNamespace"`
	DestinationBucket     string `json:"destinationBucket"`
	DestinationObjectName string `json:"destinationObjectName"`
}

// updateTierBody is the updateObjectStorageTier action request.
type updateTierBody struct {
	ObjectName  string `json:"objectName"`
	StorageTier string `json:"storageTier"`
}

// createUploadBody is the CreateMultipartUpload request.
type createUploadBody struct {
	Object      string            `json:"object"`
	ContentType string            `json:"contentType"`
	StorageTier string            `json:"storageTier"`
	Metadata    map[string]string `json:"metadata"`
}

// uploadBody is a multipart upload as OCI reports it.
type uploadBody struct {
	Namespace   string `json:"namespace"`
	Bucket      string `json:"bucket"`
	Object      string `json:"object"`
	UploadID    string `json:"uploadId"`
	TimeCreated string `json:"timeCreated"`
}

// commitPartBody names one part to commit.
type commitPartBody struct {
	PartNum int    `json:"partNum"`
	ETag    string `json:"etag"`
}

// commitUploadBody is the CommitMultipartUpload request.
type commitUploadBody struct {
	PartsToCommit  []commitPartBody `json:"partsToCommit"`
	PartsToExclude []int            `json:"partsToExclude"`
}

// partBody is one entry of ListMultipartUploadParts.
type partBody struct {
	PartNumber int    `json:"partNumber"`
	ETag       string `json:"etag"`
	Size       int64  `json:"size"`
}

// createPARBody is the CreatePreauthenticatedRequest request.
type createPARBody struct {
	Name                string `json:"name"`
	ObjectName          string `json:"objectName"`
	AccessType          string `json:"accessType"`
	BucketListingAction string `json:"bucketListingAction"`
	TimeExpires         string `json:"timeExpires"`
}

// parBody is a pre-authenticated request as OCI reports it. AccessURI is
// returned only from the create call.
type parBody struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	AccessURI           string `json:"accessUri,omitempty"`
	ObjectName          string `json:"objectName,omitempty"`
	AccessType          string `json:"accessType"`
	BucketListingAction string `json:"bucketListingAction,omitempty"`
	TimeCreated         string `json:"timeCreated"`
	TimeExpires         string `json:"timeExpires"`
	FullPath            string `json:"fullPath,omitempty"`
}

// retentionDurationBody is a retention rule's duration.
type retentionDurationBody struct {
	TimeAmount int64  `json:"timeAmount"`
	TimeUnit   string `json:"timeUnit"`
}

// retentionRuleRequestBody is the Create/UpdateRetentionRule request.
type retentionRuleRequestBody struct {
	DisplayName    string                 `json:"displayName"`
	Duration       *retentionDurationBody `json:"duration"`
	TimeRuleLocked string                 `json:"timeRuleLocked"`
}

// retentionRuleBody is a retention rule as OCI reports it.
type retentionRuleBody struct {
	ID             string                 `json:"id"`
	DisplayName    string                 `json:"displayName"`
	Duration       *retentionDurationBody `json:"duration,omitempty"`
	TimeRuleLocked string                 `json:"timeRuleLocked,omitempty"`
	TimeCreated    string                 `json:"timeCreated"`
	TimeModified   string                 `json:"timeModified"`
	ETag           string                 `json:"etag"`
}

// retentionRuleListBody is the ListRetentionRules response.
type retentionRuleListBody struct {
	Items []retentionRuleBody `json:"items"`
}

// lifecycleFilterBody is a lifecycle rule's object-name filter.
type lifecycleFilterBody struct {
	InclusionPrefixes []string `json:"inclusionPrefixes,omitempty"`
}

// lifecycleRuleBody is one OCI object lifecycle rule.
type lifecycleRuleBody struct {
	Name             string               `json:"name"`
	Action           string               `json:"action"`
	TimeAmount       int64                `json:"timeAmount"`
	TimeUnit         string               `json:"timeUnit"`
	IsEnabled        bool                 `json:"isEnabled"`
	ObjectNameFilter *lifecycleFilterBody `json:"objectNameFilter,omitempty"`
}

// lifecycleBody is the object lifecycle policy.
type lifecycleBody struct {
	Items []lifecycleRuleBody `json:"items"`
}
