package images

// ARM JSON shapes for Microsoft.Compute/images.

type imageRequest struct {
	Location   string            `json:"location"`
	Tags       map[string]string `json:"tags,omitempty"`
	Properties imageRequestProps `json:"properties"`
}

type imageRequestProps struct {
	SourceVirtualMachine *resourceRef `json:"sourceVirtualMachine,omitempty"`
}

type resourceRef struct {
	ID string `json:"id"`
}

type imageResponse struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Location   string             `json:"location"`
	Tags       map[string]string  `json:"tags,omitempty"`
	Properties imageResponseProps `json:"properties"`
}

type imageResponseProps struct {
	ProvisioningState    string               `json:"provisioningState"`
	SourceVirtualMachine *resourceRef         `json:"sourceVirtualMachine,omitempty"`
	StorageProfile       *imageStorageProfile `json:"storageProfile,omitempty"`
}

// imageStorageProfile is the storageProfile of an image resource, carrying the
// captured OS disk (and, in real Azure, data disks we do not model).
type imageStorageProfile struct {
	OSDisk *imageOSDisk `json:"osDisk,omitempty"`
}

type imageOSDisk struct {
	OSType             string `json:"osType,omitempty"`
	OSState            string `json:"osState,omitempty"`
	DiskSizeGB         int    `json:"diskSizeGB,omitempty"`
	StorageAccountType string `json:"storageAccountType,omitempty"`
}

type imageListResponse struct {
	Value []imageResponse `json:"value"`
}
