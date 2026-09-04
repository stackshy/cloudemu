// Package ec2 implements the AWS EC2 query-protocol as a server.Handler.
// Point the real aws-sdk-go-v2 EC2 client at a Server registered with this
// handler and operations work against an in-memory compute driver.
package ec2

import (
	"net/http"
	"strings"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	"github.com/stackshy/cloudemu/v2/services/cost"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// formContentType is the request Content-Type AWS SDKs use for the query
// protocol (form-encoded POST). We match this prefix rather than a strict
// equality because SDKs append "; charset=utf-8".
const formContentType = "application/x-www-form-urlencoded"

// maxFormBodyBytes caps EC2 form-encoded request bodies. Real EC2 requests
// are small (a few KB even for deeply nested TagSpecifications), so 1 MiB is
// plenty of headroom while preventing memory-exhaustion attacks.
const maxFormBodyBytes = 1 << 20

// codeInvalidInstanceID is the EC2 error code for a request naming a
// non-existent instance id.
const codeInvalidInstanceID = "InvalidInstanceID.NotFound"

// defaultAccountID is the owner account id echoed on EC2/VPC resources when the
// server is constructed without an explicit account id. It matches the AWS
// server's default so EC2 owner-ids and ARNs agree with STS/IAM/SQS/etc.
const defaultAccountID = "000000000000"

// Handler serves EC2 query-protocol requests. Real AWS EC2 serves both
// compute and VPC/networking on one endpoint, so the handler holds both
// drivers and dispatches based on the Action parameter.
type Handler struct {
	compute computedriver.Compute
	vpc     netdriver.Networking
	// accountID is the configured owner account echoed as the ownerId on
	// resources and embedded in ARNs, so EC2/VPC agree with the account id the
	// rest of the AWS server (STS/IAM/SQS/...) reports. Defaults to
	// defaultAccountID when the caller passes an empty string.
	accountID string
	// ri holds the wire-only Reserved Instance purchase/reporting surface (a
	// billing instrument, so no compute-driver method backs it). It is always
	// initialized, so the RI actions work even when the handler is built without
	// a clock/region (falling back to the real clock and a default region).
	ri *riStore
}

// Option configures optional Handler behavior. Options are additive and
// backward-compatible: existing New(c, v, accountID) callers keep working
// unchanged, and only the wire server passes clock/region for deterministic
// Reserved Instance timelines.
type Option func(*Handler)

// WithClock sets the clock driving the Reserved Instance queued/active/retired
// timeline (nil = real clock). Tests inject a config.FakeClock for determinism.
func WithClock(clock config.Clock) Option {
	return func(h *Handler) { h.ri.clock = clockOrReal(clock) }
}

// WithRegion sets the region tagging purchased reservations and the RI
// commitment scope, and seeds the offering catalog's AZ names.
func WithRegion(region string) Option {
	return func(h *Handler) {
		if region != "" {
			h.ri.region = region
			h.ri.offerings = seedRIOfferings(region)
		}
	}
}

// New returns an EC2 handler backed by c and v. Either may be nil if only
// one service is being emulated, though most workflows need both together.
// accountID is the owner account echoed on resources; an empty string falls
// back to defaultAccountID. Options configure the optional Reserved Instance
// surface (clock/region); with none the RI ops run on the real clock.
func New(c computedriver.Compute, v netdriver.Networking, accountID string, opts ...Option) *Handler {
	if accountID == "" {
		accountID = defaultAccountID
	}

	h := &Handler{
		compute:   c,
		vpc:       v,
		accountID: accountID,
		ri:        newRIStore("", nil),
	}

	for _, opt := range opts {
		opt(h)
	}

	return h
}

// clockOrReal returns clock, or a real clock when clock is nil.
func clockOrReal(clock config.Clock) config.Clock {
	if clock == nil {
		return config.RealClock{}
	}

	return clock
}

// Commitments exposes the Reserved Instance store as a cost.Commitments source,
// so a later Cost Explorer coverage/utilization handler (billing-parity step 3)
// can amortize the reservations purchased here. Union it with the Savings Plans
// source via cost.Combine to price RI and SP commitments together.
func (h *Handler) Commitments() cost.Commitments { return h.ri }

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
		h.routeNatGateways,
		h.routeVpcPeering,
		h.routeFlowLogs,
		h.routeNetworkACLs,
		h.routeTransitGateways,
		h.routeVPN,
		h.routeDHCPOptions,
		h.routePrefixLists,
		h.routeEgressOnlyIGW,
		h.routeEndpointServices,
		h.routeVPCEndpoints,
		h.routeClientVPN,
		h.routeIPAM,
		h.routeIPAMResources,
		h.routeIPAMDiscovery,
		h.routeIPAMByoip,
		h.routeIPAMResolver,
		h.routeIPAMPolicy,
		h.routeTrafficMirroring,
		h.routeNetworkInsights,
		h.routeVPCBlockPublicAccess,
		h.routePlacementGroups,
		h.routeVPC,
		h.routeTags,
		h.routeMetadata,
		h.routeInstanceStatus,
		h.routeIamInstanceProfileAssociations,
		h.routeReservedInstances,
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
	case "ModifySnapshotAttribute":
		h.modifySnapshotAttribute(w, r)
	case "DescribeSnapshotAttribute":
		h.describeSnapshotAttribute(w, r)
	case "CopySnapshot":
		h.copySnapshot(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeImages(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateImage":
		h.createImage(w, r)
	case "RegisterImage":
		h.registerImage(w, r)
	case "DeregisterImage":
		h.deregisterImage(w, r)
	case "DescribeImages":
		h.describeImages(w, r)
	case "DescribeImageAttribute":
		h.describeImageAttribute(w, r)
	case "CopyImage":
		h.copyImage(w, r)
	case "ModifyImageAttribute":
		h.modifyImageAttribute(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeNatGateways(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateNatGateway":
		h.createNatGateway(w, r)
	case "DeleteNatGateway":
		h.deleteNatGateway(w, r)
	case "DescribeNatGateways":
		h.describeNatGateways(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeVpcPeering(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateVpcPeeringConnection":
		h.createVpcPeeringConnection(w, r)
	case "AcceptVpcPeeringConnection":
		h.acceptVpcPeeringConnection(w, r)
	case "RejectVpcPeeringConnection":
		h.rejectVpcPeeringConnection(w, r)
	case "DeleteVpcPeeringConnection":
		h.deleteVpcPeeringConnection(w, r)
	case "DescribeVpcPeeringConnections":
		h.describeVpcPeeringConnections(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeFlowLogs(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateFlowLogs":
		h.createFlowLogs(w, r)
	case "DeleteFlowLogs":
		h.deleteFlowLogs(w, r)
	case "DescribeFlowLogs":
		h.describeFlowLogs(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeNetworkACLs(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateNetworkAcl":
		h.createNetworkACL(w, r)
	case "DeleteNetworkAcl":
		h.deleteNetworkACL(w, r)
	case "DescribeNetworkAcls":
		h.describeNetworkACLs(w, r)
	case "CreateNetworkAclEntry":
		h.createNetworkACLEntry(w, r)
	case "ReplaceNetworkAclEntry":
		h.replaceNetworkACLEntry(w, r)
	case "ReplaceNetworkAclAssociation":
		h.replaceNetworkACLAssociation(w, r)
	case "DeleteNetworkAclEntry":
		h.deleteNetworkACLEntry(w, r)
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
	case "ModifyLaunchTemplate":
		h.modifyLaunchTemplate(w, r)
	case "CreateLaunchTemplateVersion":
		h.createLaunchTemplateVersion(w, r)
	case "DescribeLaunchTemplateVersions":
		h.describeLaunchTemplateVersions(w, r)
	case "GetLaunchTemplateData":
		h.getLaunchTemplateData(w, r)
	default:
		return false
	}

	return true
}

//nolint:dupl // action-dispatch switch; every route* function has this shape by design
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
	case "DescribeInstanceAttribute":
		h.describeInstanceAttribute(w, r)
	case "GetConsoleOutput":
		h.getConsoleOutput(w, r)
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
	case "DescribeVolumeStatus":
		h.describeVolumeStatus(w, r)
	case "ModifyVolume":
		h.modifyVolume(w, r)
	default:
		return false
	}

	return true
}

func (h *Handler) routeKeyPairs(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateKeyPair":
		h.createKeyPair(w, r)
	case "ImportKeyPair":
		h.importKeyPair(w, r)
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

	if h.routeVPCSecurityGroupRule(w, r, action) {
		return true
	}

	if h.routeVPCInternetGateway(w, r, action) {
		return true
	}

	if h.routeVPCRouteTable(w, r, action) {
		return true
	}

	return h.routeVPCAddress(w, r, action)
}

func (h *Handler) routeVPCResource(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateVpc":
		h.createVpc(w, r)
	case "DeleteVpc":
		h.deleteVpc(w, r)
	case "ModifyVpcAttribute":
		h.modifyVpcAttribute(w, r)
	case "DescribeVpcAttribute":
		h.describeVpcAttribute(w, r)
	case "DescribeVpcs":
		h.describeVpcs(w, r)
	case "DescribeAvailabilityZones":
		h.describeAvailabilityZones(w, r)
	default:
		return false
	}

	return true
}

// routeVPCAddress dispatches Elastic IP actions. Split out of
// routeVPCResource to keep that dispatch table's cyclomatic complexity down.
func (h *Handler) routeVPCAddress(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "AllocateAddress":
		h.allocateAddress(w, r)
	case "ReleaseAddress":
		h.releaseAddress(w, r)
	case "DescribeAddresses":
		h.describeAddresses(w, r)
	case "DescribeAddressesAttribute":
		h.describeAddressesAttribute(w, r)
	case "AssociateAddress":
		h.associateAddress(w, r)
	case "DisassociateAddress":
		h.disassociateAddress(w, r)
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
	case "ModifySubnetAttribute":
		h.modifySubnetAttribute(w, r)
	default:
		return false
	}

	return true
}

//nolint:dupl // action-dispatch switch; every route* function has this shape by design
func (h *Handler) routeVPCSecurityGroup(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateSecurityGroup":
		h.createSecurityGroup(w, r)
	case "DeleteSecurityGroup":
		h.deleteSecurityGroup(w, r)
	case "DescribeSecurityGroups":
		h.describeSecurityGroups(w, r)
	case "DescribeSecurityGroupRules":
		h.describeSecurityGroupRules(w, r)
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

// routeVPCSecurityGroupRule dispatches the AWS-only security-group rule-mutation
// actions (rule full-replace + description updates). It is kept separate from
// routeVPCSecurityGroup so each dispatch table stays small.
func (h *Handler) routeVPCSecurityGroupRule(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "ModifySecurityGroupRules":
		h.modifySecurityGroupRules(w, r)
	case "UpdateSecurityGroupRuleDescriptionsIngress":
		h.updateSecurityGroupRuleDescriptions(w, r, false)
	case "UpdateSecurityGroupRuleDescriptionsEgress":
		h.updateSecurityGroupRuleDescriptions(w, r, true)
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
	case "DeleteInternetGateway":
		h.deleteInternetGateway(w, r)
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
	case "DeleteRouteTable":
		h.deleteRouteTable(w, r)
	case "DescribeRouteTables":
		h.describeRouteTables(w, r)
	case "CreateRoute":
		h.createRoute(w, r)
	case "DeleteRoute":
		h.deleteRoute(w, r)
	case "ReplaceRoute":
		h.replaceRoute(w, r)
	case "AssociateRouteTable":
		h.associateRouteTable(w, r)
	case "DisassociateRouteTable":
		h.disassociateRouteTable(w, r)
	default:
		return h.routeVPCNetworkInterfaces(w, r, action)
	}

	return true
}

func (h *Handler) routeVPCNetworkInterfaces(w http.ResponseWriter, r *http.Request, action string) bool {
	switch action {
	case "CreateNetworkInterface":
		h.createNetworkInterface(w, r)
	case "DescribeNetworkInterfaces":
		h.describeNetworkInterfaces(w, r)
	case "AttachNetworkInterface":
		h.attachNetworkInterface(w, r)
	case "DetachNetworkInterface":
		h.detachNetworkInterface(w, r)
	case "DeleteNetworkInterface":
		h.deleteNetworkInterface(w, r)
	case "ModifyNetworkInterfaceAttribute":
		h.modifyNetworkInterfaceAttribute(w, r)
	default:
		return false
	}

	return true
}

// writeErr maps CloudEmu errors to EC2 XML error responses for instance ops.
// VPC ops should use writeErrWithNotFound to emit resource-specific codes like
// "InvalidVpcID.NotFound" or "InvalidSubnetID.NotFound".
func writeErr(w http.ResponseWriter, err error) {
	writeErrWithNotFound(w, err, codeInvalidInstanceID, "IncorrectInstanceState")
}

// writeErrWithNotFound writes an error with caller-supplied NotFound and
// FailedPrecondition codes so each resource type emits the right AWS error.
func writeErrWithNotFound(w http.ResponseWriter, err error, notFoundCode, preconditionCode string) {
	msg := cerrors.Message(err)

	switch {
	case cerrors.IsNotFound(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest, notFoundCode, msg)
	case cerrors.IsAlreadyExists(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"ResourceAlreadyExists", msg)
	case cerrors.IsInvalidArgument(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"InvalidParameterValue", msg)
	case cerrors.IsFailedPrecondition(err):
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			preconditionCode, msg)
	case cerrors.GetCode(err) == cerrors.Unimplemented:
		// An unsupported optional op is a client-facing 400 InvalidAction, not a
		// 500 — matching how the launch-template ops answer an absent capability.
		awsquery.WriteXMLError(w, http.StatusBadRequest,
			"InvalidAction", msg)
	default:
		awsquery.WriteXMLError(w, http.StatusInternalServerError,
			"InternalError", msg)
	}
}
