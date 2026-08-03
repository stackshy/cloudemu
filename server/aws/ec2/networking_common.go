package ec2

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

// writeReturnTrue writes the common EC2 "<return>true</return>" acknowledgement
// with a caller-supplied response root element (set at runtime via xml.Name).
// SDK output shapes ignore unknown fields, so a return element is harmless even
// for actions whose real response carries only a requestId.
func writeReturnTrue(w http.ResponseWriter, rootElement string) {
	awsquery.WriteXMLResponse(w, struct {
		XMLName   xml.Name
		Xmlns     string `xml:"xmlns,attr"`
		RequestID string `xml:"requestId"`
		Return    bool   `xml:"return"`
	}{
		XMLName:   xml.Name{Local: rootElement},
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}
