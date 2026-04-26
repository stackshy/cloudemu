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
	ProvisioningState    string       `json:"provisioningState"`
	SourceVirtualMachine *resourceRef `json:"sourceVirtualMachine,omitempty"`
}

type imageListResponse struct {
	Value []imageResponse `json:"value"`
}
