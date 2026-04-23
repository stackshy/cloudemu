package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

type spotRequestXML struct {
	SpotInstanceRequestID string `xml:"spotInstanceRequestId"`
	State                 string `xml:"state"`
	Type                  string `xml:"type,omitempty"`
	InstanceID            string `xml:"instanceId,omitempty"`
	CreateTime            string `xml:"createTime,omitempty"`
	SpotPrice             string `xml:"spotPrice,omitempty"`
}

type requestSpotInstancesResponseXML struct {
	XMLName             xml.Name         `xml:"RequestSpotInstancesResponse"`
	Xmlns               string           `xml:"xmlns,attr"`
	RequestID           string           `xml:"requestId"`
	SpotInstanceRequest []spotRequestXML `xml:"spotInstanceRequestSet>item"`
}

type describeSpotInstanceRequestsResponseXML struct {
	XMLName             xml.Name         `xml:"DescribeSpotInstanceRequestsResponse"`
	Xmlns               string           `xml:"xmlns,attr"`
	RequestID           string           `xml:"requestId"`
	SpotInstanceRequest []spotRequestXML `xml:"spotInstanceRequestSet>item"`
}

type cancelSpotItemXML struct {
	SpotInstanceRequestID string `xml:"spotInstanceRequestId"`
	State                 string `xml:"state"`
}

type cancelSpotInstanceRequestsResponseXML struct {
	XMLName   xml.Name            `xml:"CancelSpotInstanceRequestsResponse"`
	Xmlns     string              `xml:"xmlns,attr"`
	RequestID string              `xml:"requestId"`
	Items     []cancelSpotItemXML `xml:"spotInstanceRequestSet>item"`
}

func (h *Handler) requestSpotInstances(w http.ResponseWriter, r *http.Request) {
	count, _ := strconv.Atoi(r.Form.Get("InstanceCount"))
	if count < 1 {
		count = 1
	}

	price, _ := strconv.ParseFloat(r.Form.Get("SpotPrice"), 64)

	cfg := computedriver.SpotRequestConfig{
		InstanceConfig: computedriver.InstanceConfig{
			ImageID:      r.Form.Get("LaunchSpecification.ImageId"),
			InstanceType: r.Form.Get("LaunchSpecification.InstanceType"),
		},
		MaxPrice: price,
		Count:    count,
		Type:     r.Form.Get("Type"),
	}

	reqs, err := h.compute.RequestSpotInstances(r.Context(), cfg)
	if err != nil {
		writeSpotErr(w, err)
		return
	}

	out := make([]spotRequestXML, 0, len(reqs))
	for i := range reqs {
		out = append(out, toSpotRequestXML(&reqs[i]))
	}

	awsquery.WriteXMLResponse(w, requestSpotInstancesResponseXML{
		Xmlns:               awsquery.Namespace,
		RequestID:           awsquery.RequestID,
		SpotInstanceRequest: out,
	})
}

func (h *Handler) cancelSpotInstanceRequests(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "SpotInstanceRequestId")

	if err := h.compute.CancelSpotRequests(r.Context(), ids); err != nil {
		writeSpotErr(w, err)
		return
	}

	items := make([]cancelSpotItemXML, 0, len(ids))
	for _, id := range ids {
		items = append(items, cancelSpotItemXML{SpotInstanceRequestID: id, State: "canceled"})
	}

	awsquery.WriteXMLResponse(w, cancelSpotInstanceRequestsResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Items:     items,
	})
}

//nolint:dupl // per-resource describe pattern
func (h *Handler) describeSpotInstanceRequests(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "SpotInstanceRequestId")

	reqs, err := h.compute.DescribeSpotRequests(r.Context(), ids)
	if err != nil {
		writeSpotErr(w, err)
		return
	}

	out := make([]spotRequestXML, 0, len(reqs))
	for i := range reqs {
		out = append(out, toSpotRequestXML(&reqs[i]))
	}

	awsquery.WriteXMLResponse(w, describeSpotInstanceRequestsResponseXML{
		Xmlns:               awsquery.Namespace,
		RequestID:           awsquery.RequestID,
		SpotInstanceRequest: out,
	})
}

func toSpotRequestXML(s *computedriver.SpotInstanceRequest) spotRequestXML {
	return spotRequestXML{
		SpotInstanceRequestID: s.ID,
		State:                 s.Status,
		Type:                  s.Type,
		InstanceID:            s.InstanceID,
		CreateTime:            s.CreatedAt,
		SpotPrice:             strconv.FormatFloat(s.MaxPrice, 'f', -1, 64),
	}
}

func writeSpotErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidSpotInstanceRequestID.NotFound", "IncorrectState")
}
