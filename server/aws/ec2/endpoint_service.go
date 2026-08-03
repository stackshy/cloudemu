package ec2

import (
	"encoding/xml"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

func (h *Handler) endpointServices() (netdriver.VPCEndpointServices, bool) {
	s, ok := h.vpc.(netdriver.VPCEndpointServices)

	return s, ok
}

type endpointServiceXML struct {
	ServiceID          string    `xml:"serviceId"`
	ServiceName        string    `xml:"serviceName"`
	ServiceState       string    `xml:"serviceState"`
	AcceptanceRequired bool      `xml:"acceptanceRequired"`
	AvailabilityZones  []string  `xml:"availabilityZoneSet>item,omitempty"`
	NlbArns            []string  `xml:"networkLoadBalancerArnSet>item,omitempty"`
	Tags               []tagItem `xml:"tagSet>item,omitempty"`
}

func (h *Handler) routeEndpointServices(w http.ResponseWriter, r *http.Request, action string) bool {
	s, ok := h.endpointServices()
	if !ok {
		return false
	}

	switch action {
	case "CreateVpcEndpointServiceConfiguration":
		h.createEndpointService(w, r, s)
	case "DeleteVpcEndpointServiceConfigurations":
		h.deleteEndpointService(w, r, s)
	case "DescribeVpcEndpointServiceConfigurations":
		h.describeEndpointServices(w, r, s)
	default:
		return false
	}

	return true
}

func (h *Handler) createEndpointService(w http.ResponseWriter, r *http.Request, s netdriver.VPCEndpointServices) {
	out, err := s.CreateVPCEndpointServiceConfiguration(r.Context(), netdriver.EndpointServiceConfig{
		NetworkLoadBalancerARNs: awsquery.ListStrings(r.Form, "NetworkLoadBalancerArn"),
		AcceptanceRequired:      r.Form.Get("AcceptanceRequired") == formTrue,
		Tags:                    mergeTagSpecs(awsquery.TagSpecs(r.Form), "vpc-endpoint-service"),
	})
	if err != nil {
		writeEndpointServiceErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name           `xml:"CreateVpcEndpointServiceConfigurationResponse"`
		Xmlns   string             `xml:"xmlns,attr"`
		Req     string             `xml:"requestId"`
		Config  endpointServiceXML `xml:"serviceConfiguration"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Config: toEndpointServiceXML(out)})
}

func (h *Handler) deleteEndpointService(w http.ResponseWriter, r *http.Request, s netdriver.VPCEndpointServices) {
	for _, id := range awsquery.ListStrings(r.Form, "ServiceId") {
		if err := s.DeleteVPCEndpointServiceConfiguration(r.Context(), id); err != nil {
			writeEndpointServiceErr(w, err)
			return
		}
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name `xml:"DeleteVpcEndpointServiceConfigurationsResponse"`
		Xmlns   string   `xml:"xmlns,attr"`
		Req     string   `xml:"requestId"`
		Unsucc  []string `xml:"unsuccessful>item,omitempty"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID})
}

func (h *Handler) describeEndpointServices(w http.ResponseWriter, r *http.Request, s netdriver.VPCEndpointServices) {
	items, err := s.DescribeVPCEndpointServiceConfigurations(r.Context(), awsquery.ListStrings(r.Form, "ServiceId"))
	if err != nil {
		writeEndpointServiceErr(w, err)
		return
	}

	out := make([]endpointServiceXML, 0, len(items))
	for i := range items {
		out = append(out, toEndpointServiceXML(&items[i]))
	}

	awsquery.WriteXMLResponse(w, struct {
		XMLName xml.Name             `xml:"DescribeVpcEndpointServiceConfigurationsResponse"`
		Xmlns   string               `xml:"xmlns,attr"`
		Req     string               `xml:"requestId"`
		Set     []endpointServiceXML `xml:"serviceConfigurationSet>item"`
	}{Xmlns: awsquery.Namespace, Req: awsquery.RequestID, Set: out})
}

func toEndpointServiceXML(s *netdriver.EndpointService) endpointServiceXML {
	return endpointServiceXML{
		ServiceID: s.ID, ServiceName: s.ServiceName, ServiceState: s.State,
		AcceptanceRequired: s.AcceptanceRequired, AvailabilityZones: s.AvailabilityZones,
		NlbArns: s.NetworkLoadBalancerARNs, Tags: toTagItems(s.Tags),
	}
}

func writeEndpointServiceErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidVpcEndpointServiceId.NotFound", "IncorrectState")
}
