package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

// asgNamespace is the XML namespace AutoScaling responses carry. Distinct
// from EC2's namespace — the SDK's parser tolerates either, but we emit the
// "right" one so the wire capture matches real AWS.
const asgNamespace = "http://autoscaling.amazonaws.com/doc/2011-01-01/"

type asgResponseMetadata struct {
	RequestID string `xml:"RequestId"`
}

type asgXML struct {
	Name              string    `xml:"AutoScalingGroupName"`
	MinSize           int       `xml:"MinSize"`
	MaxSize           int       `xml:"MaxSize"`
	DesiredCapacity   int       `xml:"DesiredCapacity"`
	Status            string    `xml:"Status,omitempty"`
	HealthCheckType   string    `xml:"HealthCheckType,omitempty"`
	CreatedTime       string    `xml:"CreatedTime,omitempty"`
	InstanceIDs       []string  `xml:"Instances>member>InstanceId,omitempty"`
	AvailabilityZones []string  `xml:"AvailabilityZones>member,omitempty"`
	Tags              []tagItem `xml:"Tags>member,omitempty"`
}

type createAutoScalingGroupResponseXML struct {
	XMLName          xml.Name            `xml:"CreateAutoScalingGroupResponse"`
	Xmlns            string              `xml:"xmlns,attr"`
	ResponseMetadata asgResponseMetadata `xml:"ResponseMetadata"`
}

type deleteAutoScalingGroupResponseXML struct {
	XMLName          xml.Name            `xml:"DeleteAutoScalingGroupResponse"`
	Xmlns            string              `xml:"xmlns,attr"`
	ResponseMetadata asgResponseMetadata `xml:"ResponseMetadata"`
}

type updateAutoScalingGroupResponseXML struct {
	XMLName          xml.Name            `xml:"UpdateAutoScalingGroupResponse"`
	Xmlns            string              `xml:"xmlns,attr"`
	ResponseMetadata asgResponseMetadata `xml:"ResponseMetadata"`
}

type setDesiredCapacityResponseXML struct {
	XMLName          xml.Name            `xml:"SetDesiredCapacityResponse"`
	Xmlns            string              `xml:"xmlns,attr"`
	ResponseMetadata asgResponseMetadata `xml:"ResponseMetadata"`
}

type describeAutoScalingGroupsResponseXML struct {
	XMLName xml.Name `xml:"DescribeAutoScalingGroupsResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		Groups []asgXML `xml:"AutoScalingGroups>member"`
	} `xml:"DescribeAutoScalingGroupsResult"`
	ResponseMetadata asgResponseMetadata `xml:"ResponseMetadata"`
}

type putScalingPolicyResponseXML struct {
	XMLName xml.Name `xml:"PutScalingPolicyResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct {
		PolicyARN string `xml:"PolicyARN"`
	} `xml:"PutScalingPolicyResult"`
	ResponseMetadata asgResponseMetadata `xml:"ResponseMetadata"`
}

type deleteScalingPolicyResponseXML struct {
	XMLName          xml.Name            `xml:"DeleteScalingPolicyResponse"`
	Xmlns            string              `xml:"xmlns,attr"`
	ResponseMetadata asgResponseMetadata `xml:"ResponseMetadata"`
}

type executePolicyResponseXML struct {
	XMLName          xml.Name            `xml:"ExecutePolicyResponse"`
	Xmlns            string              `xml:"xmlns,attr"`
	ResponseMetadata asgResponseMetadata `xml:"ResponseMetadata"`
}

func (h *Handler) createAutoScalingGroup(w http.ResponseWriter, r *http.Request) {
	minSize, _ := strconv.Atoi(r.Form.Get("MinSize"))
	maxSize, _ := strconv.Atoi(r.Form.Get("MaxSize"))
	desired, _ := strconv.Atoi(r.Form.Get("DesiredCapacity"))

	cfg := computedriver.AutoScalingGroupConfig{
		Name:              r.Form.Get("AutoScalingGroupName"),
		MinSize:           minSize,
		MaxSize:           maxSize,
		DesiredCapacity:   desired,
		HealthCheckType:   r.Form.Get("HealthCheckType"),
		AvailabilityZones: asgMembers(r.Form, "AvailabilityZones"),
		InstanceConfig: computedriver.InstanceConfig{
			ImageID:      r.Form.Get("LaunchConfigurationName"),
			InstanceType: "t2.micro",
		},
	}

	if _, err := h.compute.CreateAutoScalingGroup(r.Context(), cfg); err != nil {
		writeASGErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, createAutoScalingGroupResponseXML{
		Xmlns:            asgNamespace,
		ResponseMetadata: asgResponseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) deleteAutoScalingGroup(w http.ResponseWriter, r *http.Request) {
	force := r.Form.Get("ForceDelete") == "true"

	if err := h.compute.DeleteAutoScalingGroup(r.Context(),
		r.Form.Get("AutoScalingGroupName"), force); err != nil {
		writeASGErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteAutoScalingGroupResponseXML{
		Xmlns:            asgNamespace,
		ResponseMetadata: asgResponseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) updateAutoScalingGroup(w http.ResponseWriter, r *http.Request) {
	name := r.Form.Get("AutoScalingGroupName")
	minSize, _ := strconv.Atoi(r.Form.Get("MinSize"))
	maxSize, _ := strconv.Atoi(r.Form.Get("MaxSize"))
	desired, _ := strconv.Atoi(r.Form.Get("DesiredCapacity"))

	if err := h.compute.UpdateAutoScalingGroup(r.Context(), name, desired, minSize, maxSize); err != nil {
		writeASGErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, updateAutoScalingGroupResponseXML{
		Xmlns:            asgNamespace,
		ResponseMetadata: asgResponseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) setDesiredCapacity(w http.ResponseWriter, r *http.Request) {
	desired, _ := strconv.Atoi(r.Form.Get("DesiredCapacity"))

	if err := h.compute.SetDesiredCapacity(r.Context(),
		r.Form.Get("AutoScalingGroupName"), desired); err != nil {
		writeASGErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, setDesiredCapacityResponseXML{
		Xmlns:            asgNamespace,
		ResponseMetadata: asgResponseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) describeAutoScalingGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.listASGs(r)
	if err != nil {
		writeASGErr(w, err)
		return
	}

	out := make([]asgXML, 0, len(groups))

	for i := range groups {
		out = append(out, toASGXML(&groups[i]))
	}

	resp := describeAutoScalingGroupsResponseXML{
		Xmlns:            asgNamespace,
		ResponseMetadata: asgResponseMetadata{RequestID: awsquery.RequestID},
	}
	resp.Result.Groups = out
	awsquery.WriteXMLResponse(w, resp)
}

func (h *Handler) putScalingPolicy(w http.ResponseWriter, r *http.Request) {
	adjustment, _ := strconv.Atoi(r.Form.Get("ScalingAdjustment"))
	cooldown, _ := strconv.Atoi(r.Form.Get("Cooldown"))

	policy := computedriver.ScalingPolicy{
		Name:              r.Form.Get("PolicyName"),
		AutoScalingGroup:  r.Form.Get("AutoScalingGroupName"),
		PolicyType:        r.Form.Get("PolicyType"),
		AdjustmentType:    r.Form.Get("AdjustmentType"),
		ScalingAdjustment: adjustment,
		Cooldown:          cooldown,
	}

	if err := h.compute.PutScalingPolicy(r.Context(), policy); err != nil {
		writeASGErr(w, err)
		return
	}

	resp := putScalingPolicyResponseXML{
		Xmlns:            asgNamespace,
		ResponseMetadata: asgResponseMetadata{RequestID: awsquery.RequestID},
	}
	resp.Result.PolicyARN = "arn:aws:autoscaling:::" + policy.Name

	awsquery.WriteXMLResponse(w, resp)
}

func (h *Handler) deleteScalingPolicy(w http.ResponseWriter, r *http.Request) {
	err := h.compute.DeleteScalingPolicy(r.Context(),
		r.Form.Get("AutoScalingGroupName"), r.Form.Get("PolicyName"))
	if err != nil {
		writeASGErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, deleteScalingPolicyResponseXML{
		Xmlns:            asgNamespace,
		ResponseMetadata: asgResponseMetadata{RequestID: awsquery.RequestID},
	})
}

func (h *Handler) executePolicy(w http.ResponseWriter, r *http.Request) {
	err := h.compute.ExecuteScalingPolicy(r.Context(),
		r.Form.Get("AutoScalingGroupName"), r.Form.Get("PolicyName"))
	if err != nil {
		writeASGErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, executePolicyResponseXML{
		Xmlns:            asgNamespace,
		ResponseMetadata: asgResponseMetadata{RequestID: awsquery.RequestID},
	})
}

func toASGXML(g *computedriver.AutoScalingGroup) asgXML {
	return asgXML{
		Name:              g.Name,
		MinSize:           g.MinSize,
		MaxSize:           g.MaxSize,
		DesiredCapacity:   g.DesiredCapacity,
		Status:            g.Status,
		HealthCheckType:   g.HealthCheckType,
		CreatedTime:       g.CreatedAt,
		InstanceIDs:       g.InstanceIDs,
		AvailabilityZones: g.AvailabilityZones,
		Tags:              toTagItems(g.Tags),
	}
}

// asgMembers reads the AutoScaling wire form (member.N=value) vs EC2's (Foo.N=value).
func asgMembers(form map[string][]string, prefix string) []string {
	dot := prefix + ".member."

	var out []string

	for key, vals := range form {
		if len(vals) == 0 {
			continue
		}

		if len(key) > len(dot) && key[:len(dot)] == dot {
			out = append(out, vals[0])
		}
	}

	return out
}

// listASGs resolves the AutoScalingGroupNames filter and returns matching
// groups (or all if no names given). Pulled out of the describe handler to
// keep that function short and linear.
func (h *Handler) listASGs(r *http.Request) ([]computedriver.AutoScalingGroup, error) {
	names := asgMembers(r.Form, "AutoScalingGroupNames")
	if len(names) == 0 {
		return h.compute.ListAutoScalingGroups(r.Context())
	}

	var groups []computedriver.AutoScalingGroup

	for _, n := range names {
		g, err := h.compute.GetAutoScalingGroup(r.Context(), n)
		if err != nil {
			continue
		}

		groups = append(groups, *g)
	}

	return groups, nil
}

func writeASGErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "AutoScalingGroupNotFound", "ValidationError")
}
