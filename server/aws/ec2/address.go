package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Elastic IP query-protocol handlers.
//
// The driver already implemented Allocate/Release/Describe/Associate/
// Disassociate — only the wire layer was missing, so these actions returned
// "unknown action" despite the behavior existing underneath. A NAT gateway
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
	PublicIP                string `xml:"publicIp"`
	AllocationID            string `xml:"allocationId"`
	AssociationID           string `xml:"associationId,omitempty"`
	InstanceID              string `xml:"instanceId,omitempty"`
	NetworkInterfaceID      string `xml:"networkInterfaceId,omitempty"`
	NetworkInterfaceOwnerID string `xml:"networkInterfaceOwnerId,omitempty"`
	PrivateIPAddress        string `xml:"privateIpAddress,omitempty"`
	Domain                  string `xml:"domain"`
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
	// Addresses are stored by allocation id, so a PublicIp value resolves
	// against them by lookup rather than being passed through as if it were
	// one — passing it through always missed, while the comment claimed both
	// forms worked.
	id := r.Form.Get("AllocationId")
	if id == "" {
		id = h.allocationIDForPublicIP(r, r.Form.Get("PublicIp"))
	}

	if err := h.vpc.ReleaseAddress(r.Context(), id); err != nil {
		// An Elastic IP still associated (e.g. held by a NAT gateway) can't be
		// released; real EC2 answers InvalidIPAddress.InUse.
		if cerrors.IsFailedPrecondition(err) {
			awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidIPAddress.InUse", err.Error())
			return
		}

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
		addr := addressXML{
			PublicIP:           eips[i].PublicIP,
			AllocationID:       eips[i].AllocationID,
			AssociationID:      eips[i].AssociationID,
			InstanceID:         eips[i].InstanceID,
			NetworkInterfaceID: eips[i].NetworkInterfaceID,
			PrivateIPAddress:   eips[i].PrivateIP,
			Domain:             domainVPC,
		}

		// networkInterfaceOwnerId only surfaces for an ENI-bound EIP; it is the
		// account that owns the interface, which in the emulator is the single
		// default account.
		if addr.NetworkInterfaceID != "" {
			addr.NetworkInterfaceOwnerID = ownerID
		}

		out = append(out, addr)
	}

	awsquery.WriteXMLResponse(w, describeAddressesResponseXML{
		Xmlns: awsquery.Namespace, RequestID: awsquery.RequestID, AddressSet: out,
	})
}

// allocationIDForPublicIP resolves a public address back to its allocation id.
// Returns the input unchanged when it cannot be resolved, so the caller still
// gets a not-found naming what it asked for.
func (h *Handler) allocationIDForPublicIP(r *http.Request, publicIP string) string {
	if publicIP == "" {
		return ""
	}

	addrs, err := h.vpc.DescribeAddresses(r.Context(), nil)
	if err != nil {
		return publicIP
	}

	for i := range addrs {
		if addrs[i].PublicIP == publicIP {
			return addrs[i].AllocationID
		}
	}

	return publicIP
}

type associateAddressResponseXML struct {
	XMLName       xml.Name `xml:"AssociateAddressResponse"`
	Xmlns         string   `xml:"xmlns,attr"`
	RequestID     string   `xml:"requestId"`
	AssociationID string   `xml:"associationId"`
}

type disassociateAddressResponseXML struct {
	XMLName   xml.Name `xml:"DisassociateAddressResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// associateAddress binds an allocated EIP to an instance or a network
// interface.
//
// AWS accepts the EIP by AllocationId and, for callers that never held one, by
// PublicIp; the allocation ID is resolved from the public IP so both spell the
// same association. InstanceId and NetworkInterfaceId are mutually exclusive —
// supplying both is InvalidParameterCombination. An unknown InstanceId answers
// InvalidInstanceID.NotFound; the networking driver validates an unknown
// NetworkInterfaceId as InvalidNetworkInterfaceID.NotFound.
func (h *Handler) associateAddress(w http.ResponseWriter, r *http.Request) {
	allocationID := r.Form.Get("AllocationId")

	if allocationID == "" {
		allocationID = h.allocationIDForPublicIP(r, r.Form.Get("PublicIp"))
	}

	instanceID := r.Form.Get("InstanceId")
	networkInterfaceID := r.Form.Get("NetworkInterfaceId")

	if instanceID != "" && networkInterfaceID != "" {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidParameterCombination",
			"You may specify an instance ID or a network interface ID, but not both")

		return
	}

	// The networking driver does not model instances, so an unknown instance is
	// caught here against the compute driver — matching AttachNetworkInterface.
	if instanceID != "" && h.compute != nil {
		insts, err := h.compute.DescribeInstances(r.Context(), []string{instanceID}, nil)
		if err != nil || len(insts) == 0 {
			awsquery.WriteXMLError(w, http.StatusBadRequest, codeInvalidInstanceID,
				"instance "+instanceID+" not found")

			return
		}
	}

	assocID, err := h.vpc.AssociateAddress(r.Context(), allocationID, netdriver.AssociateAddressInput{
		InstanceID:         instanceID,
		NetworkInterfaceID: networkInterfaceID,
		PrivateIP:          r.Form.Get("PrivateIpAddress"),
		AllowReassociation: parseOptionalBool(r.Form.Get("AllowReassociation")),
	})
	if err != nil {
		writeAssociateAddressErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, associateAddressResponseXML{
		Xmlns:         awsquery.Namespace,
		RequestID:     awsquery.RequestID,
		AssociationID: assocID,
	})
}

// writeAssociateAddressErr maps the driver's not-found cases to the exact EC2
// codes: an unknown network interface -> InvalidNetworkInterfaceID.NotFound, an
// unknown allocation -> InvalidAllocationID.NotFound.
func writeAssociateAddressErr(w http.ResponseWriter, err error) {
	if cerrors.IsNotFound(err) && strings.Contains(err.Error(), "network interface") {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidNetworkInterfaceID.NotFound", err.Error())
		return
	}

	if cerrors.IsAlreadyExists(err) {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "Resource.AlreadyAssociated", err.Error())
		return
	}

	writeErrWithNotFound(w, err, "InvalidAllocationID.NotFound", "DependencyViolation")
}

// parseOptionalBool returns nil when the query field is absent and otherwise the
// parsed boolean, so an omitted AllowReassociation keeps AWS's default (remap)
// while an explicit false enforces the strict, fail-if-associated behavior.
func parseOptionalBool(raw string) *bool {
	if raw == "" {
		return nil
	}

	v, err := strconv.ParseBool(raw)
	if err != nil {
		return nil
	}

	return &v
}

// disassociateAddress releases an association, addressed either by
// AssociationId or — as AWS also allows — by the PublicIp holding it.
func (h *Handler) disassociateAddress(w http.ResponseWriter, r *http.Request) {
	assocID := r.Form.Get("AssociationId")

	if assocID == "" {
		if publicIP := r.Form.Get("PublicIp"); publicIP != "" {
			addrs, err := h.vpc.DescribeAddresses(r.Context(), nil)
			if err != nil {
				writeVPCErr(w, err)
				return
			}

			for i := range addrs {
				if addrs[i].PublicIP == publicIP {
					assocID = addrs[i].AssociationID
					break
				}
			}
		}
	}

	if err := h.vpc.DisassociateAddress(r.Context(), assocID); err != nil {
		writeVPCErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, disassociateAddressResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}
