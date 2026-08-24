package ec2

import (
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// asgNamespace is the XML namespace AutoScaling responses carry. Distinct
// from EC2's namespace — the SDK's parser tolerates either, but we emit the
// "right" one so the wire capture matches real AWS.
const asgNamespace = "http://autoscaling.amazonaws.com/doc/2011-01-01/"

// formTrue is the literal AWS SDKs emit for boolean true in form-encoded bodies.
const formTrue = "true"

type asgResponseMetadata struct {
	RequestID string `xml:"RequestId"`
}

// asgLaunchTemplateXML is the LaunchTemplateSpecification the SDK reads back
// from a group whose launch source is a launch template.
type asgLaunchTemplateXML struct {
	LaunchTemplateID   string `xml:"LaunchTemplateId,omitempty"`
	LaunchTemplateName string `xml:"LaunchTemplateName,omitempty"`
	Version            string `xml:"Version,omitempty"`
}

// asgInstanceXML is one member of a group's Instances list. Each running
// instance is its own <member>, so the SDK returns one Instance per instance.
type asgInstanceXML struct {
	InstanceID           string `xml:"InstanceId"`
	AvailabilityZone     string `xml:"AvailabilityZone,omitempty"`
	LifecycleState       string `xml:"LifecycleState"`
	HealthStatus         string `xml:"HealthStatus"`
	ProtectedFromScaleIn bool   `xml:"ProtectedFromScaleIn"`
}

type asgXML struct {
	Name                    string                `xml:"AutoScalingGroupName"`
	MinSize                 int                   `xml:"MinSize"`
	MaxSize                 int                   `xml:"MaxSize"`
	DesiredCapacity         int                   `xml:"DesiredCapacity"`
	Status                  string                `xml:"Status,omitempty"`
	HealthCheckType         string                `xml:"HealthCheckType,omitempty"`
	CreatedTime             string                `xml:"CreatedTime,omitempty"`
	LaunchConfigurationName string                `xml:"LaunchConfigurationName,omitempty"`
	LaunchTemplate          *asgLaunchTemplateXML `xml:"LaunchTemplate,omitempty"`
	Instances               []asgInstanceXML      `xml:"Instances>member,omitempty"`
	AvailabilityZones       []string              `xml:"AvailabilityZones>member,omitempty"`
	Tags                    []tagItem             `xml:"Tags>member,omitempty"`
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

	lcName := r.Form.Get("LaunchConfigurationName")
	ltName := r.Form.Get("LaunchTemplate.LaunchTemplateName")
	ltID := r.Form.Get("LaunchTemplate.LaunchTemplateId")

	// A group must draw its launch source from exactly one of: a launch template,
	// a launch configuration, an existing instance, or a mixed-instances policy.
	// A create with none is a client ValidationError, not a silent empty group.
	if lcName == "" && ltName == "" && ltID == "" &&
		r.Form.Get("InstanceId") == "" && !asgHasPrefix(r.Form, "MixedInstancesPolicy.") {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "ValidationError",
			"Valid requests must contain either a LaunchTemplate, LaunchConfigurationName, "+
				"InstanceId or MixedInstancesPolicy parameter.")

		return
	}

	cfg := computedriver.AutoScalingGroupConfig{
		Name:                    r.Form.Get("AutoScalingGroupName"),
		MinSize:                 minSize,
		MaxSize:                 maxSize,
		DesiredCapacity:         desired,
		HealthCheckType:         r.Form.Get("HealthCheckType"),
		AvailabilityZones:       asgMembers(r.Form, "AvailabilityZones"),
		LaunchConfigurationName: lcName,
		LaunchTemplateName:      ltName,
		LaunchTemplateID:        ltID,
		LaunchTemplateVersion:   r.Form.Get("LaunchTemplate.Version"),
		InstanceConfig: computedriver.InstanceConfig{
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
	force := r.Form.Get("ForceDelete") == formTrue

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

	// Partial update: any size property the client omits is left unchanged (and,
	// since an unchanged DesiredCapacity reconciles to a no-op, the running fleet
	// is untouched). Resolve each field against the group's current value.
	current, err := h.compute.GetAutoScalingGroup(r.Context(), name)
	if err != nil {
		writeASGErr(w, err)
		return
	}

	minSize := asgFormInt(r.Form, "MinSize", current.MinSize)
	maxSize := asgFormInt(r.Form, "MaxSize", current.MaxSize)
	desired := asgFormInt(r.Form, "DesiredCapacity", current.DesiredCapacity)

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
	x := asgXML{
		Name:                    g.Name,
		MinSize:                 g.MinSize,
		MaxSize:                 g.MaxSize,
		DesiredCapacity:         g.DesiredCapacity,
		Status:                  g.Status,
		HealthCheckType:         g.HealthCheckType,
		CreatedTime:             g.CreatedAt,
		LaunchConfigurationName: g.LaunchConfigurationName,
		Instances:               toASGInstances(g),
		AvailabilityZones:       g.AvailabilityZones,
		Tags:                    toTagItems(g.Tags),
	}

	if g.LaunchTemplateName != "" || g.LaunchTemplateID != "" {
		x.LaunchTemplate = &asgLaunchTemplateXML{
			LaunchTemplateID:   g.LaunchTemplateID,
			LaunchTemplateName: g.LaunchTemplateName,
			Version:            g.LaunchTemplateVersion,
		}
	}

	return x
}

// toASGInstances renders each running instance as its own Instances member,
// matching the AWS wire shape (one Instance per instance in the fleet).
func toASGInstances(g *computedriver.AutoScalingGroup) []asgInstanceXML {
	out := make([]asgInstanceXML, 0, len(g.InstanceIDs))

	for i, id := range g.InstanceIDs {
		inst := asgInstanceXML{
			InstanceID:     id,
			LifecycleState: "InService",
			HealthStatus:   "Healthy",
		}

		if len(g.AvailabilityZones) > 0 {
			inst.AvailabilityZone = g.AvailabilityZones[i%len(g.AvailabilityZones)]
		}

		out = append(out, inst)
	}

	return out
}

// asgFormInt returns the form value for key as an int, or fallback when the
// client omitted the field — the basis of AutoScaling's partial-update semantics.
func asgFormInt(form map[string][]string, key string, fallback int) int {
	vals, ok := form[key]
	if !ok || len(vals) == 0 || vals[0] == "" {
		return fallback
	}

	n, err := strconv.Atoi(vals[0])
	if err != nil {
		return fallback
	}

	return n
}

// asgHasPrefix reports whether any form key starts with prefix (used to detect a
// nested MixedInstancesPolicy.* launch source).
func asgHasPrefix(form map[string][]string, prefix string) bool {
	for k := range form {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}

	return false
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
	// A FailedPrecondition from the ASG driver is a delete blocked by live
	// instances (no ForceDelete) — AWS answers that with ResourceInUse (400).
	writeErrWithNotFound(w, err, "AutoScalingGroupNotFound", "ResourceInUse")
}
