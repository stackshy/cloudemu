package gcs

// GCS REST JSON shapes (https://cloud.google.com/storage/docs/json_api).
// Names map directly to the wire format the SDK expects.

type bucketResource struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	SelfLink    string `json:"selfLink,omitempty"`
	Location    string `json:"location,omitempty"`
	TimeCreated string `json:"timeCreated,omitempty"`
}

type bucketsListResponse struct {
	Kind  string           `json:"kind"`
	Items []bucketResource `json:"items"`
}

type objectResource struct {
	Kind           string            `json:"kind"`
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Bucket         string            `json:"bucket"`
	Generation     string            `json:"generation"`
	Metageneration string            `json:"metageneration"`
	ContentType    string            `json:"contentType,omitempty"`
	Size           string            `json:"size"`
	MD5Hash        string            `json:"md5Hash,omitempty"`
	ETag           string            `json:"etag,omitempty"`
	StorageClass   string            `json:"storageClass,omitempty"`
	TimeCreated    string            `json:"timeCreated,omitempty"`
	Updated        string            `json:"updated,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	SelfLink       string            `json:"selfLink,omitempty"`
	MediaLink      string            `json:"mediaLink,omitempty"`
}

type objectsListResponse struct {
	Kind          string           `json:"kind"`
	Items         []objectResource `json:"items"`
	Prefixes      []string         `json:"prefixes,omitempty"`
	NextPageToken string           `json:"nextPageToken,omitempty"`
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
