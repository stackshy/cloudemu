package gcs

// GCS REST JSON shapes (https://cloud.google.com/storage/docs/json_api).
// Names map directly to the wire format the SDK expects.

type bucketResource struct {
	Kind             string            `json:"kind"`
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	SelfLink         string            `json:"selfLink,omitempty"`
	Location         string            `json:"location,omitempty"`
	LocationType     string            `json:"locationType,omitempty"`
	StorageClass     string            `json:"storageClass,omitempty"`
	Versioning       *bucketVersioning `json:"versioning,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
	Lifecycle        *bucketLifecycle  `json:"lifecycle,omitempty"`
	IamConfiguration *iamConfiguration `json:"iamConfiguration,omitempty"`
	RetentionPolicy  *retentionPolicy  `json:"retentionPolicy,omitempty"`
	Metageneration   string            `json:"metageneration,omitempty"`
	Etag             string            `json:"etag,omitempty"`
	TimeCreated      string            `json:"timeCreated,omitempty"`
	Updated          string            `json:"updated,omitempty"`
}

type bucketVersioning struct {
	Enabled bool `json:"enabled"`
}

type bucketLifecycle struct {
	Rule []lifecycleRule `json:"rule"`
}

type lifecycleRule struct {
	Action    lifecycleAction    `json:"action"`
	Condition lifecycleCondition `json:"condition"`
}

type lifecycleAction struct {
	Type         string `json:"type"`
	StorageClass string `json:"storageClass,omitempty"`
}

// lifecycleCondition carries the full set of GCS lifecycle rule conditions
// (https://cloud.google.com/storage/docs/lifecycle#conditions). Age and the
// day-count conditions are pointers so an explicit 0 round-trips distinctly
// from an absent condition.
type lifecycleCondition struct {
	Age                     *int     `json:"age,omitempty"`
	CreatedBefore           string   `json:"createdBefore,omitempty"`
	CustomTimeBefore        string   `json:"customTimeBefore,omitempty"`
	DaysSinceCustomTime     *int     `json:"daysSinceCustomTime,omitempty"`
	DaysSinceNoncurrentTime *int     `json:"daysSinceNoncurrentTime,omitempty"`
	NoncurrentTimeBefore    string   `json:"noncurrentTimeBefore,omitempty"`
	IsLive                  *bool    `json:"isLive,omitempty"`
	MatchesStorageClass     []string `json:"matchesStorageClass,omitempty"`
	NumNewerVersions        *int     `json:"numNewerVersions,omitempty"`
	MatchesPrefix           []string `json:"matchesPrefix,omitempty"`
	MatchesSuffix           []string `json:"matchesSuffix,omitempty"`
}

type iamConfiguration struct {
	UniformBucketLevelAccess *uniformBucketLevelAccess `json:"uniformBucketLevelAccess,omitempty"`
	PublicAccessPrevention   string                    `json:"publicAccessPrevention,omitempty"`
}

// retentionPolicy is the bucket retentionPolicy sub-resource (WORM). GCS encodes
// retentionPeriod as a string-typed int64 (seconds); effectiveTime is RFC3339;
// isLocked reports whether the policy has been made permanent.
type retentionPolicy struct {
	RetentionPeriod string `json:"retentionPeriod,omitempty"`
	EffectiveTime   string `json:"effectiveTime,omitempty"`
	IsLocked        bool   `json:"isLocked,omitempty"`
}

type uniformBucketLevelAccess struct {
	Enabled bool `json:"enabled"`
	// LockedTime is when UBLA becomes permanent; GCS stamps it ~90 days out when
	// UBLA is enabled and omits it when disabled.
	LockedTime string `json:"lockedTime,omitempty"`
}

type bucketsListResponse struct {
	Kind  string           `json:"kind"`
	Items []bucketResource `json:"items"`
}

type objectResource struct {
	Kind                    string            `json:"kind"`
	ID                      string            `json:"id"`
	Name                    string            `json:"name"`
	Bucket                  string            `json:"bucket"`
	Generation              string            `json:"generation"`
	Metageneration          string            `json:"metageneration"`
	ContentType             string            `json:"contentType,omitempty"`
	Size                    string            `json:"size"`
	MD5Hash                 string            `json:"md5Hash,omitempty"`
	CRC32C                  string            `json:"crc32c,omitempty"`
	ETag                    string            `json:"etag,omitempty"`
	StorageClass            string            `json:"storageClass,omitempty"`
	CacheControl            string            `json:"cacheControl,omitempty"`
	ContentEncoding         string            `json:"contentEncoding,omitempty"`
	ContentDisposition      string            `json:"contentDisposition,omitempty"`
	ContentLanguage         string            `json:"contentLanguage,omitempty"`
	TimeCreated             string            `json:"timeCreated,omitempty"`
	Updated                 string            `json:"updated,omitempty"`
	Metadata                map[string]string `json:"metadata,omitempty"`
	TemporaryHold           bool              `json:"temporaryHold,omitempty"`
	EventBasedHold          bool              `json:"eventBasedHold,omitempty"`
	RetentionExpirationTime string            `json:"retentionExpirationTime,omitempty"`
	SelfLink                string            `json:"selfLink,omitempty"`
	MediaLink               string            `json:"mediaLink,omitempty"`
}

type objectsListResponse struct {
	Kind          string           `json:"kind"`
	Items         []objectResource `json:"items"`
	Prefixes      []string         `json:"prefixes,omitempty"`
	NextPageToken string           `json:"nextPageToken,omitempty"`
}

// objectPatchBody is the Objects: patch/update request. Pointer fields let the
// handler tell "field absent" from "field set to empty"; metadata values are
// pointers so a null entry deletes that custom-metadata key (GCS merge patch).
type objectPatchBody struct {
	ContentType        *string            `json:"contentType"`
	CacheControl       *string            `json:"cacheControl"`
	ContentEncoding    *string            `json:"contentEncoding"`
	ContentDisposition *string            `json:"contentDisposition"`
	ContentLanguage    *string            `json:"contentLanguage"`
	Metadata           map[string]*string `json:"metadata"`
	TemporaryHold      *bool              `json:"temporaryHold"`
	EventBasedHold     *bool              `json:"eventBasedHold"`
}

// composeRequest is the Objects: compose request body.
type composeRequest struct {
	SourceObjects []composeSource `json:"sourceObjects"`
	Destination   *objectResource `json:"destination,omitempty"`
}

type composeSource struct {
	Name string `json:"name"`
	// Generation is JSON-encoded as a string by the GCS API / Go storage SDK
	// (like objectResource.Generation), so it needs the ,string option.
	Generation int64 `json:"generation,omitempty,string"`
}

// iamPolicyResource is the storage#policy document (Buckets: get/setIamPolicy).
type iamPolicyResource struct {
	Kind       string          `json:"kind"`
	ResourceID string          `json:"resourceId"`
	Version    int             `json:"version"`
	Bindings   []iamPolicyBind `json:"bindings"`
	Etag       string          `json:"etag"`
}

type iamPolicyBind struct {
	Role    string   `json:"role"`
	Members []string `json:"members"`
}

// testPermissionsResponse is the Buckets: testIamPermissions response.
type testPermissionsResponse struct {
	Kind        string   `json:"kind"`
	Permissions []string `json:"permissions"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    int           `json:"code"`
	Message string        `json:"message"`
	Errors  []errorDetail `json:"errors,omitempty"`
	Status  string        `json:"status,omitempty"`
}

type errorDetail struct {
	Domain  string `json:"domain"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}
