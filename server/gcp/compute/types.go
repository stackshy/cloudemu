package compute

// GCP REST JSON shapes for compute#instance.
//
// Modeled on the public schema (https://cloud.google.com/compute/docs/reference/rest/v1/instances)
// with only the fields we need today. Optional fields like serviceAccounts,
// scheduling, metadata, etc. are intentionally omitted — extending is purely
// additive when needed.

// instanceRequest is the inbound shape for POST .../instances (Insert).
type instanceRequest struct {
	Name              string             `json:"name"`
	MachineType       string             `json:"machineType"`
	Disks             []attachedDisk     `json:"disks,omitempty"`
	NetworkInterfaces []networkInterface `json:"networkInterfaces,omitempty"`
	Tags              tagsBlock          `json:"tags,omitempty"`
	Labels            map[string]string  `json:"labels,omitempty"`
}

type attachedDisk struct {
	Boot             bool                  `json:"boot,omitempty"`
	AutoDelete       bool                  `json:"autoDelete,omitempty"`
	InitializeParams *diskInitializeParams `json:"initializeParams,omitempty"`
}

type diskInitializeParams struct {
	SourceImage string `json:"sourceImage,omitempty"`
	DiskType    string `json:"diskType,omitempty"`
}

type networkInterface struct {
	Network    string `json:"network,omitempty"`
	Subnetwork string `json:"subnetwork,omitempty"`
}

type tagsBlock struct {
	Items []string `json:"items,omitempty"`
}

// instanceResponse is the outbound shape for GET single, GET list element.
type instanceResponse struct {
	Kind              string             `json:"kind"`
	ID                string             `json:"id"`
	Name              string             `json:"name"`
	MachineType       string             `json:"machineType"`
	Status            string             `json:"status"`
	Zone              string             `json:"zone"`
	SelfLink          string             `json:"selfLink"`
	NetworkInterfaces []networkInterface `json:"networkInterfaces,omitempty"`
	Labels            map[string]string  `json:"labels,omitempty"`
	CreationTimestamp string             `json:"creationTimestamp,omitempty"`
}

// instanceListResponse is the outbound shape for GET .../instances.
type instanceListResponse struct {
	Kind     string             `json:"kind"`
	ID       string             `json:"id"`
	Items    []instanceResponse `json:"items"`
	SelfLink string             `json:"selfLink"`
}
