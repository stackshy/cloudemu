package ec2

import (
	"encoding/xml"
	"net/http"

	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
)

// Elastic IP query-protocol handlers.
//
// The driver already implemented Allocate/Release/Describe/Associate/
// Disassociate — only the wire layer was missing, so these actions returned
// "unknown action" despite the behaviour existing underneath. A NAT gateway
// cannot be created without first allocating an EIP, which makes this the
// first hard stop in every VPC-with-private-subnets plan.

type allocateAddressResponseXML struct {
	XMLName      xml.Name `xml:"AllocateAddressResponse"`
	Xmlns        string   `xml:"xmlns,attr"`
	RequestID    string   `xml:"requestId"`
	PublicIP     string   `xml:"publicIp"`
	AllocationID string   `xml:"allocationId"`
	Domain       string   `xml:"domain"`
}

type releaseAddressResponseXML struct {
	XMLName   xml.Name `xml:"ReleaseAddressResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

type addressXML struct {
	PublicIP      string `xml:"publicIp"`
	AllocationID  string `xml:"allocationId"`
	AssociationID string `xml:"associationId,omitempty"`
	InstanceID    string `xml:"instanceId,omitempty"`
	Domain        string `xml:"domain"`
}

type describeAddressesResponseXML struct {
	XMLName    xml.Name     `xml:"DescribeAddressesResponse"`
	Xmlns      string       `xml:"xmlns,attr"`
	RequestID  string       `xml:"requestId"`
	AddressSet []addressXML `xml:"addressesSet>item"`
}

// domainVPC is the only domain modern accounts allocate in — EC2-Classic was
// retired in 2022. Reporting it unconditionally matches what real AWS returns
// and keeps callers from branching on a value that can no longer vary.
const domainVPC = "vpc"

func (h *Handler) allocateAddress(w http.ResponseWriter, r *http.Request) {
	eip, err := h.vpc.AllocateAddress(r.Context(), netdriver.ElasticIPConfig{
		Tags: mergeTagSpecs(awsquery.TagSpecs(r.Form), "elastic-ip"),
	})
	if err != nil {
		writeVPCErr(w, err)

		return
	}

	awsquery.WriteXMLResponse(w, allocateAddressResponseXML{
		Xmlns:        awsquery.Namespace,
		RequestID:    awsquery.RequestID,
		PublicIP:     eip.PublicIP,
		AllocationID: eip.AllocationID,
		Domain:       domainVPC,
	})
}

func (h *Handler) releaseAddress(w http.ResponseWriter, r *http.Request) {
	// AWS accepts either AllocationId (VPC) or PublicIp (Classic). Only the
	// former is meaningful post-EC2-Classic, but callers still send whichever
	// their SDK version prefers, so both are read.
	id := r.Form.Get("AllocationId")
	if id == "" {
		id = r.Form.Get("PublicIp")
	}

	if err := h.vpc.ReleaseAddress(r.Context(), id); err != nil {
		writeVPCErr(w, err)

		return
	}

	awsquery.WriteXMLResponse(w, releaseAddressResponseXML{
		Xmlns: awsquery.Namespace, RequestID: awsquery.RequestID, Return: true,
	})
}

func (h *Handler) describeAddresses(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "AllocationId")

	eips, err := h.vpc.DescribeAddresses(r.Context(), ids)
	if err != nil {
		writeVPCErr(w, err)

		return
	}

	out := make([]addressXML, 0, len(eips))
	for i := range eips {
		out = append(out, addressXML{
			PublicIP:      eips[i].PublicIP,
			AllocationID:  eips[i].AllocationID,
			AssociationID: eips[i].AssociationID,
			InstanceID:    eips[i].InstanceID,
			Domain:        domainVPC,
		})
	}

	awsquery.WriteXMLResponse(w, describeAddressesResponseXML{
		Xmlns: awsquery.Namespace, RequestID: awsquery.RequestID, AddressSet: out,
	})
}
