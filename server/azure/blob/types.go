package blob

import "encoding/xml"

// Azure Blob storage XML response shapes. Field names match the wire
// protocol exactly because the SDK unmarshalling is strict about them.

// listContainersResult is the body for GET /?comp=list.
type listContainersResult struct {
	XMLName    xml.Name       `xml:"EnumerationResults"`
	Prefix     string         `xml:"Prefix,omitempty"`
	Marker     string         `xml:"Marker,omitempty"`
	MaxResults int            `xml:"MaxResults,omitempty"`
	Containers containersList `xml:"Containers"`
	NextMarker string         `xml:"NextMarker"`
}

type containersList struct {
	Containers []containerXML `xml:"Container"`
}

type containerXML struct {
	Name       string            `xml:"Name"`
	Properties containerPropsXML `xml:"Properties"`
}

type containerPropsXML struct {
	LastModified string `xml:"Last-Modified"`
	ETag         string `xml:"Etag"`
}

// listBlobsResult is the body for GET /{container}?restype=container&comp=list.
type listBlobsResult struct {
	XMLName         xml.Name  `xml:"EnumerationResults"`
	ServiceEndpoint string    `xml:"ServiceEndpoint,attr,omitempty"`
	ContainerName   string    `xml:"ContainerName,attr"`
	Prefix          string    `xml:"Prefix,omitempty"`
	Marker          string    `xml:"Marker,omitempty"`
	MaxResults      int       `xml:"MaxResults,omitempty"`
	Delimiter       string    `xml:"Delimiter,omitempty"`
	Blobs           blobsList `xml:"Blobs"`
	NextMarker      string    `xml:"NextMarker"`
}

type blobsList struct {
	Blobs        []blobXML       `xml:"Blob"`
	BlobPrefixes []blobPrefixXML `xml:"BlobPrefix,omitempty"`
}

type blobXML struct {
	Name       string       `xml:"Name"`
	Properties blobPropsXML `xml:"Properties"`
	Metadata   *metadataXML `xml:"Metadata,omitempty"`
}

type blobPropsXML struct {
	LastModified  string `xml:"Last-Modified"`
	ETag          string `xml:"Etag"`
	ContentLength int64  `xml:"Content-Length"`
	ContentType   string `xml:"Content-Type"`
	BlobType      string `xml:"BlobType"`
}

type blobPrefixXML struct {
	Name string `xml:"Name"`
}

type metadataXML struct {
	// Metadata is dynamic — encoded inline as child elements during marshal.
	// We store as a map and use a custom marshaller below.
	Items map[string]string `xml:"-"`
}

// MarshalXML emits each metadata item as a child element.
func (m metadataXML) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	if err := e.EncodeToken(start); err != nil {
		return err
	}

	for k, v := range m.Items {
		t := xml.StartElement{Name: xml.Name{Local: k}}
		if err := e.EncodeElement(v, t); err != nil {
			return err
		}
	}

	return e.EncodeToken(xml.EndElement{Name: start.Name})
}

// errorXML is the Azure Blob error envelope.
type errorXML struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}
