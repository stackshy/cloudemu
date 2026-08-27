package compute

// GCP REST JSON shapes for compute#instance.
//
// Modeled on the public schema (https://cloud.google.com/compute/docs/reference/rest/v1/instances).
// Fields are the subset CloudEmu round-trips; extending is purely additive.

// instanceRequest is the inbound shape for POST .../instances (Insert).
type instanceRequest struct {
	Name              string             `json:"name"`
	MachineType       string             `json:"machineType"`
	Disks             []attachedDisk     `json:"disks,omitempty"`
	NetworkInterfaces []networkInterface `json:"networkInterfaces,omitempty"`
	Tags              tagsBlock          `json:"tags,omitempty"`
	Labels            map[string]string  `json:"labels,omitempty"`
	Metadata          metadataBlock      `json:"metadata,omitempty"`
	ServiceAccounts   []serviceAccount   `json:"serviceAccounts,omitempty"`
}

// metadataBlock is GCP's instance metadata. The boot script is carried as a
// metadata item with key "startup-script"
// (https://cloud.google.com/compute/docs/instances/startup-scripts/linux).
type metadataBlock struct {
	Kind        string         `json:"kind,omitempty"`
	Items       []metadataItem `json:"items,omitempty"`
	Fingerprint string         `json:"fingerprint,omitempty"`
}

type metadataItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// attachedDisk models compute#attachedDisk on both the request (client sets
// boot/autoDelete/deviceName/initializeParams) and the response (we echo the
// resolved source self-link, diskSizeGb, type, mode).
type attachedDisk struct {
	Kind             string                `json:"kind,omitempty"`
	Boot             bool                  `json:"boot,omitempty"`
	AutoDelete       bool                  `json:"autoDelete,omitempty"`
	DeviceName       string                `json:"deviceName,omitempty"`
	Index            int                   `json:"index,omitempty"`
	Source           string                `json:"source,omitempty"`
	DiskSizeGb       string                `json:"diskSizeGb,omitempty"`
	Type             string                `json:"type,omitempty"`
	Mode             string                `json:"mode,omitempty"`
	Interface        string                `json:"interface,omitempty"`
	InitializeParams *diskInitializeParams `json:"initializeParams,omitempty"`
}

type diskInitializeParams struct {
	SourceImage string `json:"sourceImage,omitempty"`
	DiskType    string `json:"diskType,omitempty"`
	DiskSizeGb  string `json:"diskSizeGb,omitempty"`
}

type networkInterface struct {
	Name          string         `json:"name,omitempty"`
	Network       string         `json:"network,omitempty"`
	Subnetwork    string         `json:"subnetwork,omitempty"`
	NetworkIP     string         `json:"networkIP,omitempty"`
	StackType     string         `json:"stackType,omitempty"`
	AccessConfigs []accessConfig `json:"accessConfigs,omitempty"`
}

// accessConfig models compute#accessConfig — the external-IP mapping on a
// network interface. A ONE_TO_ONE_NAT config carries the instance's public IP:
// when natIP names a reserved compute#address that address flips RESERVED->IN_USE
// while the instance holds it; when natIP is omitted GCP assigns an ephemeral
// external IP, which CloudEmu synthesizes at insert time so a GET reflects one.
type accessConfig struct {
	Kind        string `json:"kind,omitempty"`
	Type        string `json:"type,omitempty"`
	Name        string `json:"name,omitempty"`
	NatIP       string `json:"natIP,omitempty"`
	NetworkTier string `json:"networkTier,omitempty"`
}

type tagsBlock struct {
	Items       []string `json:"items,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
}

// scheduling models compute#scheduling (a realistic default block).
type scheduling struct {
	AutomaticRestart  bool   `json:"automaticRestart"`
	OnHostMaintenance string `json:"onHostMaintenance,omitempty"`
	Preemptible       bool   `json:"preemptible"`
	ProvisioningModel string `json:"provisioningModel,omitempty"`
}

// serviceAccount models compute#serviceAccount.
type serviceAccount struct {
	Email  string   `json:"email"`
	Scopes []string `json:"scopes,omitempty"`
}

// shieldedInstanceConfig models compute#shieldedInstanceConfig.
type shieldedInstanceConfig struct {
	EnableSecureBoot          bool `json:"enableSecureBoot"`
	EnableVtpm                bool `json:"enableVtpm"`
	EnableIntegrityMonitoring bool `json:"enableIntegrityMonitoring"`
}

// instanceResponse is the outbound shape for GET single, GET list element.
type instanceResponse struct {
	Kind                   string                  `json:"kind"`
	ID                     string                  `json:"id"`
	Name                   string                  `json:"name"`
	MachineType            string                  `json:"machineType"`
	Status                 string                  `json:"status"`
	Zone                   string                  `json:"zone"`
	SelfLink               string                  `json:"selfLink"`
	CreationTimestamp      string                  `json:"creationTimestamp,omitempty"`
	CPUPlatform            string                  `json:"cpuPlatform,omitempty"`
	LastStartTimestamp     string                  `json:"lastStartTimestamp,omitempty"`
	DeletionProtection     bool                    `json:"deletionProtection"`
	Disks                  []attachedDisk          `json:"disks,omitempty"`
	NetworkInterfaces      []networkInterface      `json:"networkInterfaces,omitempty"`
	Labels                 map[string]string       `json:"labels,omitempty"`
	LabelFingerprint       string                  `json:"labelFingerprint,omitempty"`
	Fingerprint            string                  `json:"fingerprint,omitempty"`
	Tags                   *tagsBlock              `json:"tags,omitempty"`
	Metadata               *metadataBlock          `json:"metadata,omitempty"`
	Scheduling             *scheduling             `json:"scheduling,omitempty"`
	ServiceAccounts        []serviceAccount        `json:"serviceAccounts,omitempty"`
	ShieldedInstanceConfig *shieldedInstanceConfig `json:"shieldedInstanceConfig,omitempty"`
}

// instanceListResponse is the outbound shape for GET .../instances.
type instanceListResponse struct {
	Kind          string             `json:"kind"`
	ID            string             `json:"id"`
	Items         []instanceResponse `json:"items"`
	NextPageToken string             `json:"nextPageToken,omitempty"`
	SelfLink      string             `json:"selfLink"`
}

// instancesScopedList is one scope's bucket in an aggregated list.
type instancesScopedList struct {
	Instances []instanceResponse `json:"instances,omitempty"`
	Warning   *scopedListWarning `json:"warning,omitempty"`
}

type scopedListWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// aggregatedListResponse is the outbound shape for GET .../aggregated/instances.
type aggregatedListResponse struct {
	Kind          string                         `json:"kind"`
	ID            string                         `json:"id"`
	Items         map[string]instancesScopedList `json:"items"`
	NextPageToken string                         `json:"nextPageToken,omitempty"`
	SelfLink      string                         `json:"selfLink"`
}

// setLabelsRequest is the body of instances.setLabels.
type setLabelsRequest struct {
	Labels           map[string]string `json:"labels"`
	LabelFingerprint string            `json:"labelFingerprint,omitempty"`
}

// setMachineTypeRequest is the body of instances.setMachineType.
type setMachineTypeRequest struct {
	MachineType string `json:"machineType"`
}

// serialPortOutput is the outbound shape for
// GET .../instances/{name}/serialPort — the response of
// instances.getSerialPortOutput
// (https://cloud.google.com/compute/docs/reference/rest/v1/instances/getSerialPortOutput).
type serialPortOutput struct {
	Kind     string `json:"kind"`
	Contents string `json:"contents"`
	Start    string `json:"start"`
	Next     string `json:"next"`
	SelfLink string `json:"selfLink"`
}
