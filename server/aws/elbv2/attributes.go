package elbv2

import (
	"encoding/xml"
	"net/http"
	"strconv"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// Attribute keys AWS models explicitly on a load balancer. Everything else is
// carried verbatim in LBAttributes.Extra.
const (
	attrIdleTimeout        = "idle_timeout.timeout_seconds"
	attrDeletionProtection = "deletion_protection.enabled"
	attrAccessLogsEnabled  = "access_logs.s3.enabled"
	attrAccessLogsBucket   = "access_logs.s3.bucket"
)

type lbAttributeXML struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type lbAttributesResult struct {
	Attributes []lbAttributeXML `xml:"Attributes>member"`
}

type modifyLBAttributesResponse struct {
	XMLName  xml.Name           `xml:"ModifyLoadBalancerAttributesResponse"`
	Xmlns    string             `xml:"xmlns,attr"`
	Result   lbAttributesResult `xml:"ModifyLoadBalancerAttributesResult"`
	Metadata responseMetadata   `xml:"ResponseMetadata"`
}

type describeLBAttributesResponse struct {
	XMLName  xml.Name           `xml:"DescribeLoadBalancerAttributesResponse"`
	Xmlns    string             `xml:"xmlns,attr"`
	Result   lbAttributesResult `xml:"DescribeLoadBalancerAttributesResult"`
	Metadata responseMetadata   `xml:"ResponseMetadata"`
}

// modifyLoadBalancerAttributes merges the supplied attributes into whatever
// the load balancer already carries.
//
// AWS treats this as a partial update — a caller enabling cross-zone must not
// silently clear an idle timeout it set earlier — so the existing attributes
// are read first and only the supplied keys are overwritten.
func (h *Handler) modifyLoadBalancerAttributes(w http.ResponseWriter, r *http.Request) {
	arn := r.Form.Get("LoadBalancerArn")

	current, err := h.lb.GetLBAttributes(r.Context(), arn)
	if err != nil {
		writeErr(w, err)
		return
	}

	attrs := *current
	if attrs.Extra == nil {
		attrs.Extra = map[string]string{}
	}

	for key, value := range parseAttributeMembers(r) {
		applyLBAttribute(&attrs, key, value)
	}

	if err := h.lb.PutLBAttributes(r.Context(), arn, attrs); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyLBAttributesResponse{
		Xmlns:    Namespace,
		Result:   lbAttributesResult{Attributes: toAttributeXML(&attrs)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeLoadBalancerAttributes(w http.ResponseWriter, r *http.Request) {
	attrs, err := h.lb.GetLBAttributes(r.Context(), r.Form.Get("LoadBalancerArn"))
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, describeLBAttributesResponse{
		Xmlns:    Namespace,
		Result:   lbAttributesResult{Attributes: toAttributeXML(attrs)},
		Metadata: responseMetadata{RequestID: awsquery.RequestID},
	})
}

// parseAttributeMembers reads the Attributes.member.N.Key/Value pairs the SDK
// serialises.
func parseAttributeMembers(r *http.Request) map[string]string {
	out := map[string]string{}

	for i := 1; ; i++ {
		prefix := "Attributes.member." + strconv.Itoa(i)

		key := r.Form.Get(prefix + ".Key")
		if key == "" {
			break
		}

		out[key] = r.Form.Get(prefix + ".Value")
	}

	return out
}

// applyLBAttribute routes a key onto its typed field when there is one, and
// into Extra otherwise so nothing supplied is lost.
func applyLBAttribute(attrs *lbdriver.LBAttributes, key, value string) {
	switch key {
	case attrIdleTimeout:
		if n, err := strconv.Atoi(value); err == nil {
			attrs.IdleTimeout = n
		}
	case attrDeletionProtection:
		attrs.DeletionProtection = value == "true"
	case attrAccessLogsEnabled:
		attrs.AccessLogsEnabled = value == "true"
	case attrAccessLogsBucket:
		attrs.AccessLogsBucket = value
	default:
		attrs.Extra[key] = value
	}
}

func toAttributeXML(attrs *lbdriver.LBAttributes) []lbAttributeXML {
	out := []lbAttributeXML{
		{Key: attrIdleTimeout, Value: strconv.Itoa(attrs.IdleTimeout)},
		{Key: attrDeletionProtection, Value: strconv.FormatBool(attrs.DeletionProtection)},
		{Key: attrAccessLogsEnabled, Value: strconv.FormatBool(attrs.AccessLogsEnabled)},
	}

	if attrs.AccessLogsBucket != "" {
		out = append(out, lbAttributeXML{Key: attrAccessLogsBucket, Value: attrs.AccessLogsBucket})
	}

	for k, v := range attrs.Extra {
		out = append(out, lbAttributeXML{Key: k, Value: v})
	}

	return out
}
