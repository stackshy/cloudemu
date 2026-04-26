package snapshots

// ARM JSON shapes for Microsoft.Compute/snapshots.

type snapshotRequest struct {
	Location   string               `json:"location"`
	Tags       map[string]string    `json:"tags,omitempty"`
	Properties snapshotRequestProps `json:"properties"`
}

type snapshotRequestProps struct {
	CreationData *creationData `json:"creationData,omitempty"`
	DiskSizeGB   int           `json:"diskSizeGB,omitempty"`
}

type creationData struct {
	CreateOption     string `json:"createOption,omitempty"`
	SourceURI        string `json:"sourceUri,omitempty"`
	SourceResourceID string `json:"sourceResourceId,omitempty"`
}

type snapshotResponse struct {
	ID         string                `json:"id"`
	Name       string                `json:"name"`
	Type       string                `json:"type"`
	Location   string                `json:"location"`
	Tags       map[string]string     `json:"tags,omitempty"`
	Properties snapshotResponseProps `json:"properties"`
}

type snapshotResponseProps struct {
	ProvisioningState string        `json:"provisioningState"`
	DiskSizeGB        int           `json:"diskSizeGB"`
	DiskState         string        `json:"diskState"`
	CreationData      *creationData `json:"creationData,omitempty"`
}

type snapshotListResponse struct {
	Value []snapshotResponse `json:"value"`
}
