package elasticache

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	cachedriver "github.com/stackshy/cloudemu/v2/services/cache/driver"
)

type cacheSubnetXML struct {
	SubnetIdentifier string `xml:"SubnetIdentifier"`
}

type cacheSubnetGroupXML struct {
	CacheSubnetGroupName        string           `xml:"CacheSubnetGroupName"`
	CacheSubnetGroupDescription string           `xml:"CacheSubnetGroupDescription,omitempty"`
	VpcID                       string           `xml:"VpcId,omitempty"`
	ARN                         string           `xml:"ARN,omitempty"`
	Subnets                     []cacheSubnetXML `xml:"Subnets>Subnet,omitempty"`
}

type cacheSubnetGroupResult struct {
	CacheSubnetGroup cacheSubnetGroupXML `xml:"CacheSubnetGroup"`
}

type createCacheSubnetGroupResponse struct {
	XMLName  xml.Name               `xml:"CreateCacheSubnetGroupResponse"`
	Xmlns    string                 `xml:"xmlns,attr"`
	Result   cacheSubnetGroupResult `xml:"CreateCacheSubnetGroupResult"`
	Metadata responseMetadata       `xml:"ResponseMetadata"`
}

type cacheSubnetGroupsList struct {
	CacheSubnetGroups []cacheSubnetGroupXML `xml:"CacheSubnetGroups>CacheSubnetGroup"`
}

type describeCacheSubnetGroupsResponse struct {
	XMLName  xml.Name              `xml:"DescribeCacheSubnetGroupsResponse"`
	Xmlns    string                `xml:"xmlns,attr"`
	Result   cacheSubnetGroupsList `xml:"DescribeCacheSubnetGroupsResult"`
	Metadata responseMetadata      `xml:"ResponseMetadata"`
}

type deleteCacheSubnetGroupResponse struct {
	XMLName  xml.Name         `xml:"DeleteCacheSubnetGroupResponse"`
	Xmlns    string           `xml:"xmlns,attr"`
	Metadata responseMetadata `xml:"ResponseMetadata"`
}

// subnetGroups reports whether the configured driver models cache subnet
// groups — an AWS-only resource, so a driver for another cloud legitimately
// does not.
func (h *Handler) subnetGroups() (cachedriver.SubnetGroups, bool) {
	sg, ok := h.cache.(cachedriver.SubnetGroups)

	return sg, ok
}

func (h *Handler) createCacheSubnetGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.subnetGroups()
	if !ok {
		writeUnsupported(w, "cache subnet groups")
		return
	}

	sg, err := store.CreateCacheSubnetGroup(r.Context(), cachedriver.SubnetGroupConfig{
		Name:        r.Form.Get("CacheSubnetGroupName"),
		Description: r.Form.Get("CacheSubnetGroupDescription"),
		SubnetIDs:   awsquery.ListStrings(r.Form, "SubnetIds.SubnetIdentifier"),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createCacheSubnetGroupResponse{
		Xmlns:    Namespace,
		Result:   cacheSubnetGroupResult{CacheSubnetGroup: toCacheSubnetGroupXML(sg)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

//nolint:dupl // per-resource describe pattern; the sibling reads the other collection
func (h *Handler) describeCacheSubnetGroups(w http.ResponseWriter, r *http.Request) {
	store, ok := h.subnetGroups()
	if !ok {
		writeUnsupported(w, "cache subnet groups")
		return
	}

	var names []string
	if n := r.Form.Get("CacheSubnetGroupName"); n != "" {
		names = []string{n}
	}

	groups, err := store.DescribeCacheSubnetGroups(r.Context(), names)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := make([]cacheSubnetGroupXML, 0, len(groups))
	for i := range groups {
		out = append(out, toCacheSubnetGroupXML(&groups[i]))
	}

	awsquery.WriteXMLResponse(w, describeCacheSubnetGroupsResponse{
		Xmlns:    Namespace,
		Result:   cacheSubnetGroupsList{CacheSubnetGroups: out},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteCacheSubnetGroup(w http.ResponseWriter, r *http.Request) {
	store, ok := h.subnetGroups()
	if !ok {
		writeUnsupported(w, "cache subnet groups")
		return
	}

	if err := store.DeleteCacheSubnetGroup(r.Context(), r.Form.Get("CacheSubnetGroupName")); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteCacheSubnetGroupResponse{
		Xmlns:    Namespace,
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func toCacheSubnetGroupXML(sg *cachedriver.SubnetGroup) cacheSubnetGroupXML {
	x := cacheSubnetGroupXML{
		CacheSubnetGroupName:        sg.Name,
		CacheSubnetGroupDescription: sg.Description,
		VpcID:                       sg.VPCID,
		ARN:                         sg.ARN,
	}

	for _, id := range sg.SubnetIDs {
		x.Subnets = append(x.Subnets, cacheSubnetXML{SubnetIdentifier: id})
	}

	return x
}

// writeUnsupported reports a capability this driver does not implement.
//
// The code is InvalidAction because that is what the service answers for an
// operation it does not serve — and because a caller matching on the code sees
// this, not the message. Routing it through the generic error mapping would
// have produced InvalidParameterValue while the message claimed otherwise.
func writeUnsupported(w http.ResponseWriter, what string) {
	awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidAction",
		"this driver does not model "+what)
}
