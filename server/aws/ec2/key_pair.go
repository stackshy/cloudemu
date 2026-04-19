package ec2

import (
	"encoding/xml"
	"net/http"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

// keyPairSummaryXML is one <item> in DescribeKeyPairs responses. It omits
// private-key material — that only comes back on CreateKeyPair.
type keyPairSummaryXML struct {
	KeyPairID      string    `xml:"keyPairId"`
	KeyName        string    `xml:"keyName"`
	KeyFingerprint string    `xml:"keyFingerprint"`
	KeyType        string    `xml:"keyType,omitempty"`
	Tags           []tagItem `xml:"tagSet>item,omitempty"`
}

// createKeyPairResponseXML inlines private-key material — AWS only returns it
// on create, never on describe.
type createKeyPairResponseXML struct {
	XMLName        xml.Name `xml:"CreateKeyPairResponse"`
	Xmlns          string   `xml:"xmlns,attr"`
	RequestID      string   `xml:"requestId"`
	KeyPairID      string   `xml:"keyPairId"`
	KeyName        string   `xml:"keyName"`
	KeyFingerprint string   `xml:"keyFingerprint"`
	KeyMaterial    string   `xml:"keyMaterial"`
	KeyType        string   `xml:"keyType,omitempty"`
}

type describeKeyPairsResponseXML struct {
	XMLName   xml.Name            `xml:"DescribeKeyPairsResponse"`
	Xmlns     string              `xml:"xmlns,attr"`
	RequestID string              `xml:"requestId"`
	KeySet    []keyPairSummaryXML `xml:"keySet>item"`
}

type deleteKeyPairResponseXML struct {
	XMLName   xml.Name `xml:"DeleteKeyPairResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

func (h *Handler) createKeyPair(w http.ResponseWriter, r *http.Request) {
	cfg := computedriver.KeyPairConfig{
		Name:    r.Form.Get("KeyName"),
		KeyType: r.Form.Get("KeyType"),
		Tags:    mergeTagSpecs(awsquery.TagSpecs(r.Form), "key-pair"),
	}

	info, err := h.compute.CreateKeyPair(r.Context(), cfg)
	if err != nil {
		writeKeyPairErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createKeyPairResponseXML{
		Xmlns:          awsquery.Namespace,
		RequestID:      awsquery.RequestID,
		KeyPairID:      info.ID,
		KeyName:        info.Name,
		KeyFingerprint: info.Fingerprint,
		KeyMaterial:    info.PrivateKey,
		KeyType:        info.KeyType,
	})
}

func (h *Handler) deleteKeyPair(w http.ResponseWriter, r *http.Request) {
	if err := h.compute.DeleteKeyPair(r.Context(), r.Form.Get("KeyName")); err != nil {
		writeKeyPairErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteKeyPairResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

//nolint:dupl // per-resource describe pattern; siblings in vpc/subnet/sg/igw/route_table/volume
func (h *Handler) describeKeyPairs(w http.ResponseWriter, r *http.Request) {
	names := awsquery.ListStrings(r.Form, "KeyName")

	pairs, err := h.compute.DescribeKeyPairs(r.Context(), names)
	if err != nil {
		writeKeyPairErr(w, err)
		return
	}

	out := make([]keyPairSummaryXML, 0, len(pairs))
	for i := range pairs {
		out = append(out, toKeyPairSummaryXML(&pairs[i]))
	}

	awsquery.WriteXMLResponse(w, describeKeyPairsResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		KeySet:    out,
	})
}

func toKeyPairSummaryXML(kp *computedriver.KeyPairInfo) keyPairSummaryXML {
	return keyPairSummaryXML{
		KeyPairID:      kp.ID,
		KeyName:        kp.Name,
		KeyFingerprint: kp.Fingerprint,
		KeyType:        kp.KeyType,
		Tags:           toTagItems(kp.Tags),
	}
}

func writeKeyPairErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidKeyPair.NotFound", "IncorrectState")
}
