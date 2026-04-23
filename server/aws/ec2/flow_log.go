package ec2

import (
	"encoding/xml"
	"net/http"

	netdriver "github.com/stackshy/cloudemu/networking/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

type flowLogXML struct {
	FlowLogID     string    `xml:"flowLogId"`
	ResourceID    string    `xml:"resourceId"`
	ResourceType  string    `xml:"resourceType"`
	TrafficType   string    `xml:"trafficType"`
	FlowLogStatus string    `xml:"flowLogStatus"`
	CreationTime  string    `xml:"creationTime,omitempty"`
	Tags          []tagItem `xml:"tagSet>item,omitempty"`
}

type createFlowLogsResponseXML struct {
	XMLName    xml.Name `xml:"CreateFlowLogsResponse"`
	Xmlns      string   `xml:"xmlns,attr"`
	RequestID  string   `xml:"requestId"`
	FlowLogIDs []string `xml:"flowLogIdSet>item"`
}

type describeFlowLogsResponseXML struct {
	XMLName    xml.Name     `xml:"DescribeFlowLogsResponse"`
	Xmlns      string       `xml:"xmlns,attr"`
	RequestID  string       `xml:"requestId"`
	FlowLogSet []flowLogXML `xml:"flowLogSet>item"`
}

type deleteFlowLogsResponseXML struct {
	XMLName      xml.Name `xml:"DeleteFlowLogsResponse"`
	Xmlns        string   `xml:"xmlns,attr"`
	RequestID    string   `xml:"requestId"`
	Unsuccessful []string `xml:"unsuccessful>item,omitempty"`
}

func (h *Handler) createFlowLogs(w http.ResponseWriter, r *http.Request) {
	resourceIDs := awsquery.ListStrings(r.Form, "ResourceId")
	if len(resourceIDs) == 0 {
		writeFlowLogErr(w, newInvalidParameterErr("ResourceId is required"))
		return
	}

	var created []string

	for _, rid := range resourceIDs {
		cfg := netdriver.FlowLogConfig{
			ResourceID:   rid,
			ResourceType: r.Form.Get("ResourceType"),
			TrafficType:  r.Form.Get("TrafficType"),
			Tags:         mergeTagSpecs(awsquery.TagSpecs(r.Form), "vpc-flow-log"),
		}

		info, err := h.vpc.CreateFlowLog(r.Context(), cfg)
		if err != nil {
			writeFlowLogErr(w, err)
			return
		}

		created = append(created, info.ID)
	}

	awsquery.WriteXMLResponse(w, createFlowLogsResponseXML{
		Xmlns:      awsquery.Namespace,
		RequestID:  awsquery.RequestID,
		FlowLogIDs: created,
	})
}

func (h *Handler) deleteFlowLogs(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "FlowLogId")

	for _, id := range ids {
		if err := h.vpc.DeleteFlowLog(r.Context(), id); err != nil {
			writeFlowLogErr(w, err)
			return
		}
	}

	awsquery.WriteXMLResponse(w, deleteFlowLogsResponseXML{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
	})
}

//nolint:dupl // per-resource describe pattern
func (h *Handler) describeFlowLogs(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "FlowLogId")

	logs, err := h.vpc.DescribeFlowLogs(r.Context(), ids)
	if err != nil {
		writeFlowLogErr(w, err)
		return
	}

	out := make([]flowLogXML, 0, len(logs))
	for i := range logs {
		out = append(out, toFlowLogXML(&logs[i]))
	}

	awsquery.WriteXMLResponse(w, describeFlowLogsResponseXML{
		Xmlns:      awsquery.Namespace,
		RequestID:  awsquery.RequestID,
		FlowLogSet: out,
	})
}

func toFlowLogXML(f *netdriver.FlowLog) flowLogXML {
	status := f.Status
	if status == "" {
		status = "ACTIVE"
	}

	return flowLogXML{
		FlowLogID:     f.ID,
		ResourceID:    f.ResourceID,
		ResourceType:  f.ResourceType,
		TrafficType:   f.TrafficType,
		FlowLogStatus: status,
		CreationTime:  f.CreatedAt,
		Tags:          toTagItems(f.Tags),
	}
}

func writeFlowLogErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidFlowLogId.NotFound", "DependencyViolation")
}
