// Package ec2 implements the AWS EC2 query-protocol as a server.Handler.
// Point the real aws-sdk-go-v2 EC2 client at a Server registered with this
// handler and operations work against an in-memory compute driver.
package ec2

import (
	"net/http"
	"strings"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
	"github.com/stackshy/cloudemu/server/wire/awsquery"
)

// formContentType is the request Content-Type AWS SDKs use for the query
// protocol (form-encoded POST). We match this prefix rather than a strict
// equality because SDKs append "; charset=utf-8".
const formContentType = "application/x-www-form-urlencoded"

// maxFormBodyBytes caps EC2 form-encoded request bodies. Real EC2 requests
// are small (a few KB even for deeply nested TagSpecifications), so 1 MiB is
// plenty of headroom while preventing memory-exhaustion attacks.
const maxFormBodyBytes = 1 << 20

// Handler serves EC2 query-protocol requests. Real AWS EC2 serves both
// compute and VPC/networking on one endpoint, so the handler holds both
// drivers and dispatches based on the Action parameter.
type Handler struct {
	compute computedriver.Compute
	vpc     netdriver.Networking
}

// New returns an EC2 handler backed by c and v. Either may be nil if only
// one service is being emulated, though most workflows need both together.
func New(c computedriver.Compute, v netdriver.Networking) *Handler {
	return &Handler{compute: c, vpc: v}
}

// Matches returns true for EC2-shaped requests. EC2 uses the AWS query
// protocol: either a POST with form-encoded body (the SDK default) or a GET
// with ?Action=... on the URL. It never sets X-Amz-Target; that's reserved
// for JSON-RPC services like DynamoDB.
func (*Handler) Matches(r *http.Request) bool {
	if r.Header.Get("X-Amz-Target") != "" {
		return false
	}

	if r.URL.Query().Get("Action") != "" {
		return true
	}

	if r.Method == http.MethodPost &&
		strings.HasPrefix(r.Header.Get("Content-Type"), formContentType) {
		return true
	}

	return false
}

// routeFunc is a per-resource dispatcher that returns true when it handled
// the action. ServeHTTP iterates a list of these so adding a new resource
// means appending one function rather than editing the main handler.
type routeFunc func(w http.ResponseWriter, r *http.Request, action string) bool

// ServeHTTP parses the request form and dispatches on Action.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBodyBytes)

	if err := r.ParseForm(); err != nil {
		awsquery.WriteXMLError(w, http.StatusBadRequest, "InvalidRequest", err.Error())
		return
	}

	action := r.Form.Get("Action")

	routes := []routeFunc{
		h.routeInstances,
		h.routeVolumes,
		h.routeKeyPairs,
		h.routeAutoScaling,
		h.routeSnapshots,
		h.routeImages,
		h.routeSpot,
		h.routeLaunchTemplates,
		h.routeVPC,
	}
	for _, route := range routes {
		if route(w, r, action) {
			return
		}
	}

	awsquery.WriteXMLError(w, http.StatusBadRequest,
		"InvalidAction", "unknown action: "+action)
}

func (h *Handler) routeSnapshots(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateSnapshot":
		h.createSnapshot(w, r)
	case "DeleteSnapshot":
		h.deleteSnapshot(w, r)
	case "DescribeSnapshots":
		h.describeSnapshots(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeImages(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateImage":
		h.createImage(w, r)
	case "DeregisterImage":
		h.deregisterImage(w, r)
	case "DescribeImages":
		h.describeImages(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeSpot(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "RequestSpotInstances":
		h.requestSpotInstances(w, r)
	case "CancelSpotInstanceRequests":
		h.cancelSpotInstanceRequests(w, r)
	case "DescribeSpotInstanceRequests":
		h.describeSpotInstanceRequests(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeLaunchTemplates(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateLaunchTemplate":
		h.createLaunchTemplate(w, r)
	case "DeleteLaunchTemplate":
		h.deleteLaunchTemplate(w, r)
	case "DescribeLaunchTemplates":
		h.describeLaunchTemplates(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeAutoScaling(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateAutoScalingGroup":
		h.createAutoScalingGroup(w, r)
	case "UpdateAutoScalingGroup":
		h.updateAutoScalingGroup(w, r)
	case "DeleteAutoScalingGroup":
		h.deleteAutoScalingGroup(w, r)
	case "DescribeAutoScalingGroups":
		h.describeAutoScalingGroups(w, r)
	case "SetDesiredCapacity":
		h.setDesiredCapacity(w, r)
	case "PutScalingPolicy":
		h.putScalingPolicy(w, r)
	case "DeletePolicy":
		h.deleteScalingPolicy(w, r)
	case "ExecutePolicy":
		h.executePolicy(w, r)
	default:
		return false
	}

	return true
}

// routeInstances dispatches instance-lifecycle actions backed by the compute
// driver. Returns true if the action was handled.
func (h *Handler) routeInstances(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "RunInstances":
		h.runInstances(w, r)
	case "DescribeInstances":
		h.describeInstances(w, r)
	case "StartInstances":
		h.startInstances(w, r)
	case "StopInstances":
		h.stopInstances(w, r)
	case "RebootInstances":
		h.rebootInstances(w, r)
	case "TerminateInstances":
		h.terminateInstances(w, r)
	case "ModifyInstanceAttribute":
		h.modifyInstanceAttribute(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeVolumes(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateVolume":
		h.createVolume(w, r)
	case "DeleteVolume":
		h.deleteVolume(w, r)
	case "DescribeVolumes":
		h.describeVolumes(w, r)
	case "AttachVolume":
		h.attachVolume(w, r)
	case "DetachVolume":
		h.detachVolume(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeKeyPairs(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateKeyPair":
		h.createKeyPair(w, r)
	case "DeleteKeyPair":
		h.deleteKeyPair(w, r)
	case "DescribeKeyPairs":
		h.describeKeyPairs(w, r)
	default:
		return false
	}

	return true
}

// routeVPC dispatches VPC/networking-driver-backed actions. Returns true if
// the action was handled. Split into per-resource sub-routers to keep
// individual dispatch tables small.
func (h *Handler) routeVPC(w http.ResponseWriter, r *http.Request, action string) bool {
	if h.routeVPCResource(w, r, action) {
		return true
	}

	if h.routeVPCSubnet(w, r, action) {
		return true
	}

	if h.routeVPCSecurityGroup(w, r, action) {
		return true
	}

	if h.routeVPCInternetGateway(w, r, action) {
		return true
	}

	return h.routeVPCRouteTable(w, r, action)
}

func (h *Handler) routeVPCResource(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateVpc":
		h.createVpc(w, r)
	case "DeleteVpc":
		h.deleteVpc(w, r)
	case "DescribeVpcs":
		h.describeVpcs(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeVPCSubnet(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateSubnet":
		h.createSubnet(w, r)
	case "DeleteSubnet":
		h.deleteSubnet(w, r)
	case "DescribeSubnets":
		h.describeSubnets(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeVPCSecurityGroup(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateSecurityGroup":
		h.createSecurityGroup(w, r)
	case "DeleteSecurityGroup":
		h.deleteSecurityGroup(w, r)
	case "DescribeSecurityGroups":
		h.describeSecurityGroups(w, r)
	case "AuthorizeSecurityGroupIngress":
		h.authorizeSecurityGroupIngress(w, r)
	case "AuthorizeSecurityGroupEgress":
		h.authorizeSecurityGroupEgress(w, r)
	case "RevokeSecurityGroupIngress":
		h.revokeSecurityGroupIngress(w, r)
	case "RevokeSecurityGroupEgress":
		h.revokeSecurityGroupEgress(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeVPCInternetGateway(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateInternetGateway":
		h.createInternetGateway(w, r)
	case "AttachInternetGateway":
		h.attachInternetGateway(w, r)
	case "DetachInternetGateway":
		h.detachInternetGateway(w, r)
	case "DescribeInternetGateways":
		h.describeInternetGateways(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeVPCRouteTable(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateRouteTable":
		h.createRouteTable(w, r)
	case "DescribeRouteTables":
		h.describeRouteTables(w, r)
	case "CreateRoute":
		h.createRoute(w, r)
	default:
		return false
	}

	return true
}

// writeErr maps CloudEmu errors to EC2 XML error responses for instance ops.
// VPC ops should use writeErrWithNotFound to emit resource-specific codes like
// "InvalidVpcID.NotFound" or "InvalidSubnetID.NotFound".
func writeErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, "InvalidInstanceID.NotFound", "IncorrectInstanceState")
}

// writeErrWithNotFound writes an error with caller-supplied NotFound and
// FailedPrecondition codes so each resource type emits the right AWS error.
func writeErrWithNotFound(w http.ResponseWriter, err error, notFoundCode, preconditionCode string) {
	switch {
	case cerrors.IsNotFound(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, notFoundCode, err.Error())
	case cerrors.IsAlreadyExists(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"ResourceAlreadyExists", err.Error())
	case cerrors.IsInvalidArgument(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"InvalidParameterValue", err.Error())
	case cerrors.IsFailedPrecondition(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			preconditionCode, err.Error())
	default:
		awsquery.WriteXMLError(w, http.StatusInternalServerError,
			"InternalError", err.Error())
	}
}
