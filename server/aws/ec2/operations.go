package ec2

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/awsquery"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	netdriver "github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// instanceAttributer is an AWS-specific optional capability for reading and
// writing instance attributes (DisableApiTermination, SourceDestCheck, …) that
// are not part of the portable Compute driver. The handler type-asserts for it
// so providers that don't support these attributes are unaffected.
type instanceAttributer interface {
	SetInstanceAttribute(ctx context.Context, id, name, value string) error
	GetInstanceAttribute(ctx context.Context, id, name string) (string, error)
	SetInstanceSecurityGroups(ctx context.Context, id string, groupIDs []string) error
}

// Attribute element names shared by ModifyInstanceAttribute (Name.Value on the
// wire) and DescribeInstanceAttribute (Attribute= selector).
const (
	attrDisableAPITermination = "disableApiTermination"
	attrSourceDestCheck       = "sourceDestCheck"
	attrInstanceType          = "instanceType"
	attrUserData              = "userData"
	attrEbsOptimized          = "ebsOptimized"
	attrGroupSet              = "groupSet"
	attrMonitoring            = "monitoring"
)

// reservationPrefix is the AWS reservation ID prefix (r-xxxx).
const reservationPrefix = "r-"

// Static instance facts real EC2 reports for the Linux/x86 instances this
// emulator launches. They are fixed rather than modeled because no cloudemu
// behavior depends on them, but SDK clients and IaC read them.
const (
	archX86            = "x86_64"
	hypervisorXen      = "xen"
	virtualizationHVM  = "hvm"
	rootDeviceTypeEBS  = "ebs"
	rootDeviceNameXVDA = "/dev/xvda"
	tenancyDefault     = "default"
	defaultZone        = "us-east-1a"
	// eniAttachedStatus / primaryDeviceIndex describe the primary ENI's
	// attachment.
	eniAttachedStatus  = "attached"
	primaryDeviceIndex = 0
	// internalDNSSuffix is the private-DNS suffix EC2 uses in us-east-1.
	internalDNSSuffix = ".ec2.internal"
)

// runInstances handles Action=RunInstances.
func (h *Handler) runInstances(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	count, err := instanceCount(form.Get("MinCount"), form.Get("MaxCount"))
	if err != nil {
		writeErr(w, err)
		return
	}

	// A LaunchTemplate reference supplies the base instance parameters (real EC2
	// resolves the template's default/requested version). Explicit RunInstances
	// parameters below override the template's data.
	cfg, err := h.launchTemplateBaseConfig(r.Context(), form)
	if err != nil {
		writeLaunchTemplateErr(w, err)
		return
	}

	applyRunInstancesForm(&cfg, form)

	instances, err := h.compute.RunInstances(r.Context(), cfg, count)
	if err != nil {
		writeErr(w, err)
		return
	}

	if len(instances) == 0 {
		writeErr(w, cerrors.New(cerrors.FailedPrecondition,
			"driver returned zero instances"))

		return
	}

	awsquery.WriteXMLResponse(w, runInstancesResponse{
		Xmlns:         awsquery.Namespace,
		RequestID:     awsquery.RequestID,
		ReservationID: reservationIDFor(&instances[0]),
		OwnerID:       ownerID,
		Instances:     h.toInstanceXMLs(r.Context(), instances),
	})
}

// launchTemplateBaseConfig resolves the InstanceConfig a RunInstances call
// inherits from its LaunchTemplate reference. It returns a zero config when no
// template is referenced (or the provider has no launch-template versioning).
// LaunchTemplate.Version selects a specific version; absent, the template's
// default version is used.
func (h *Handler) launchTemplateBaseConfig(ctx context.Context, form url.Values) (computedriver.InstanceConfig, error) {
	name := form.Get("LaunchTemplate.LaunchTemplateName")
	id := form.Get("LaunchTemplate.LaunchTemplateId")

	if name == "" && id == "" {
		return computedriver.InstanceConfig{}, nil
	}

	versioner, ok := h.compute.(computedriver.LaunchTemplateVersioner)
	if !ok {
		return computedriver.InstanceConfig{}, nil
	}

	version := form.Get("LaunchTemplate.Version")
	if version == "" {
		version = "$Default"
	}

	versions, err := versioner.DescribeLaunchTemplateVersions(ctx, computedriver.DescribeLaunchTemplateVersionsInput{
		Name:     name,
		ID:       id,
		Versions: []string{version},
	})
	if err != nil {
		return computedriver.InstanceConfig{}, err
	}

	if len(versions) == 0 {
		return computedriver.InstanceConfig{}, cerrors.Newf(cerrors.NotFound,
			"launch template version %q not found", version)
	}

	return versions[0].InstanceConfig, nil
}

// applyRunInstancesForm overlays the explicit RunInstances request parameters
// onto cfg (which may already carry launch-template data). A present parameter
// overrides the template; an absent one leaves the template value in place.
func applyRunInstancesForm(cfg *computedriver.InstanceConfig, form url.Values) {
	if v := form.Get("ImageId"); v != "" {
		cfg.ImageID = v
	}

	if v := form.Get("InstanceType"); v != "" {
		cfg.InstanceType = v
	}

	if v := form.Get("SubnetId"); v != "" {
		cfg.SubnetID = v
	}

	if sgs := awsquery.ListStrings(form, "SecurityGroupId"); len(sgs) > 0 {
		cfg.SecurityGroups = sgs
	}

	if v := form.Get("KeyName"); v != "" {
		cfg.KeyName = v
	}

	if v := form.Get("UserData"); v != "" {
		cfg.UserData = decodeUserData(v)
	}

	cfg.ClientToken = form.Get("ClientToken")

	if v := form.Get("IamInstanceProfile.Arn"); v != "" {
		cfg.IamInstanceProfileARN = v
	}

	if v := form.Get("IamInstanceProfile.Name"); v != "" {
		cfg.IamInstanceProfileName = v
	}

	if tags := mergeTagSpecs(awsquery.TagSpecs(form), "instance"); len(tags) > 0 {
		cfg.Tags = tags
	}
}

// reservationIDFor returns the instance's reservation id, falling back to a
// per-instance reservation for providers (Azure/GCP) that don't group launches.
func reservationIDFor(inst *computedriver.Instance) string {
	if inst.ReservationID != "" {
		return inst.ReservationID
	}

	return reservationPrefix + stripInstancePrefix(inst.ID)
}

// describeInstances handles Action=DescribeInstances.
func (h *Handler) describeInstances(w http.ResponseWriter, r *http.Request) {
	form := r.Form

	ids := awsquery.ListStrings(form, "InstanceId")
	filters := toDriverFilters(awsquery.Filters(form))

	includeManaged, _ := strconv.ParseBool(form.Get("IncludeManagedResources"))

	instances, err := h.compute.DescribeInstances(r.Context(), ids, filters,
		computedriver.DescribeInstancesOptions{IncludeManagedResources: includeManaged})
	if err != nil {
		writeErr(w, err)
		return
	}

	// Group instances launched by one RunInstances call under a shared
	// reservation, preserving first-seen order (real EC2 <reservationSet>).
	reservations := h.groupReservations(r.Context(), instances)

	page, next := paginateReservations(reservations, form.Get("MaxResults"), form.Get("NextToken"))

	awsquery.WriteXMLResponse(w, describeInstancesResponse{
		Xmlns:        awsquery.Namespace,
		RequestID:    awsquery.RequestID,
		Reservations: page,
		NextToken:    next,
	})
}

// groupReservations collapses instances into reservations by ReservationID,
// keeping the order reservations were first seen so paging is stable.
func (h *Handler) groupReservations(ctx context.Context, instances []computedriver.Instance) []reservationXML {
	order := make([]string, 0)
	byID := make(map[string]*reservationXML)

	for i := range instances {
		rid := reservationIDFor(&instances[i])

		res, ok := byID[rid]
		if !ok {
			order = append(order, rid)
			byID[rid] = &reservationXML{ReservationID: rid, OwnerID: ownerID}
			res = byID[rid]
		}

		res.Instances = append(res.Instances, h.toInstanceXMLs(ctx, instances[i:i+1])...)
	}

	out := make([]reservationXML, 0, len(order))
	for _, rid := range order {
		out = append(out, *byID[rid])
	}

	// Sort by reservation id so the paging cursor is stable across calls (the
	// underlying store iterates in map order). Ids are monotonic, so this is
	// also launch order.
	sort.Slice(out, func(i, j int) bool { return out[i].ReservationID < out[j].ReservationID })

	return out
}

// maxDescribeResults bounds a page when the request omits a valid MaxResults.
const maxDescribeResults = 1000

// paginateReservations slices reservations to at most maxResults, returning the
// page and a NextToken (the id of the first reservation on the following page,
// base64-encoded). An empty/invalid maxResults returns everything.
func paginateReservations(reservations []reservationXML, maxResultsStr, token string) (page []reservationXML, next string) {
	return paginateXML(reservations, maxResultsStr, token,
		func(r reservationXML) string { return r.ReservationID })
}

// paginateXML slices items to at most maxResults, returning the page and a
// NextToken (the base64-encoded id of the first item on the following page).
// An empty/invalid maxResults returns everything from the token offset, and the
// NextToken is empty on the last page. idOf yields each item's stable id, which
// is both the cursor and the value callers sort on before paging. The EC2
// Describe* handlers share it so pagination behaves identically across the VPC
// family and DescribeInstances.
func paginateXML[X any](items []X, maxResultsStr, token string, idOf func(X) string) (page []X, next string) {
	start := decodePageToken(token, items, idOf)

	limit, err := strconv.Atoi(maxResultsStr)
	if err != nil || limit <= 0 {
		return items[start:], ""
	}

	if limit > maxDescribeResults {
		limit = maxDescribeResults
	}

	end := start + limit
	if end >= len(items) {
		return items[start:], ""
	}

	return items[start:end], encodePageToken(idOf(items[end]))
}

// decodePageToken maps a NextToken back to a start index. An unknown or empty
// token starts at the beginning.
func decodePageToken[X any](token string, items []X, idOf func(X) string) int {
	if token == "" {
		return 0
	}

	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return 0
	}

	for i := range items {
		if idOf(items[i]) == string(raw) {
			return i
		}
	}

	return 0
}

// encodePageToken base64-encodes an item id for use as a NextToken.
func encodePageToken(id string) string {
	return base64.StdEncoding.EncodeToString([]byte(id))
}

// startInstances handles Action=StartInstances.
func (h *Handler) startInstances(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "InstanceId")

	prev := h.priorStates(r.Context(), ids)

	if err := h.compute.StartInstances(r.Context(), ids); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, startInstancesResponse{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Changes: stateChangesFrom(ids, prev,
			instanceState{Code: stateCodePending, Name: "pending"}),
	})
}

// stopInstances handles Action=StopInstances.
func (h *Handler) stopInstances(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "InstanceId")

	prev := h.priorStates(r.Context(), ids)

	if err := h.compute.StopInstances(r.Context(), ids); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, stopInstancesResponse{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Changes: stateChangesFrom(ids, prev,
			instanceState{Code: stateCodeStopping, Name: "stopping"}),
	})
}

// rebootInstances handles Action=RebootInstances.
func (h *Handler) rebootInstances(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "InstanceId")

	if err := h.compute.RebootInstances(r.Context(), ids); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, rebootInstancesResponse{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

// terminateInstances handles Action=TerminateInstances.
func (h *Handler) terminateInstances(w http.ResponseWriter, r *http.Request) {
	ids := awsquery.ListStrings(r.Form, "InstanceId")

	prev := h.priorStates(r.Context(), ids)

	if err := h.compute.TerminateInstances(r.Context(), ids); err != nil {
		// Termination protection surfaces as OperationNotPermitted, not the
		// generic instance-state error.
		if cerrors.IsPermissionDenied(err) {
			awsquery.WriteXMLError(w, http.StatusBadRequest, "OperationNotPermitted", err.Error())
			return
		}

		writeErr(w, err)

		return
	}

	awsquery.WriteXMLResponse(w, terminateInstancesResponse{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Changes: stateChangesFrom(ids, prev,
			instanceState{Code: stateCodeShuttingDown, Name: "shutting-down"}),
	})
}

// modifyInstanceAttribute handles Action=ModifyInstanceAttribute. InstanceType
// changes route through the portable driver (which enforces the stopped
// precondition); DisableApiTermination / SourceDestCheck route through the
// AWS-specific instanceAttributer so they take effect (previously accepted and
// silently discarded — a false success dangerous for IaC).
func (h *Handler) modifyInstanceAttribute(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("InstanceId")

	if instanceType := r.Form.Get("InstanceType.Value"); instanceType != "" {
		if err := h.compute.ModifyInstance(r.Context(),
			id, computedriver.ModifyInstanceInput{InstanceType: instanceType}); err != nil {
			writeErr(w, err)
			return
		}
	}

	if err := h.applyInstanceAttributes(r, id); err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, modifyInstanceAttributeResponse{
		Xmlns:     awsquery.Namespace,
		RequestID: awsquery.RequestID,
		Return:    true,
	})
}

// applyInstanceAttributes writes the single-value instance attributes and the
// security-group membership present in the request through the
// instanceAttributer capability. Each ModifyInstanceAttribute call carries at
// most one attribute, so absent parameters are simply skipped.
func (h *Handler) applyInstanceAttributes(r *http.Request, id string) error {
	attributer, ok := h.compute.(instanceAttributer)
	if !ok {
		return nil
	}

	for _, name := range []string{attrDisableAPITermination, attrSourceDestCheck, attrEbsOptimized, attrUserData} {
		if v := r.Form.Get(attrForm(name) + ".Value"); v != "" {
			if err := attributer.SetInstanceAttribute(r.Context(), id, name, v); err != nil {
				return err
			}
		}
	}

	// GroupId.N (VPC instances) replaces the instance's security-group set.
	if groups := awsquery.ListStrings(r.Form, "GroupId"); len(groups) > 0 {
		if err := attributer.SetInstanceSecurityGroups(r.Context(), id, groups); err != nil {
			return err
		}
	}

	return nil
}

// describeInstanceAttribute handles Action=DescribeInstanceAttribute, returning
// the single attribute named by Attribute= so ModifyInstanceAttribute changes
// are verifiable.
func (h *Handler) describeInstanceAttribute(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("InstanceId")
	attr := r.Form.Get("Attribute")

	attributer, ok := h.compute.(instanceAttributer)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "instance attributes are not supported"))
		return
	}

	resp := describeInstanceAttributeResponse{
		Xmlns:      awsquery.Namespace,
		RequestID:  awsquery.RequestID,
		InstanceID: id,
	}

	// groupSet is a list, not a single scalar, so it is read from the instance's
	// membership rather than the scalar GetInstanceAttribute path.
	if attr == attrGroupSet {
		if err := h.attachInstanceGroups(r.Context(), id, &resp); err != nil {
			writeErr(w, err)
			return
		}

		awsquery.WriteXMLResponse(w, resp)

		return
	}

	val, err := attributer.GetInstanceAttribute(r.Context(), id, attr)
	if err != nil {
		writeErr(w, err)
		return
	}

	switch attr {
	case attrDisableAPITermination:
		resp.DisableAPITermination = &attributeBooleanValueXML{Value: val == formTrue}
	case attrSourceDestCheck:
		resp.SourceDestCheck = &attributeBooleanValueXML{Value: val == formTrue}
	case attrEbsOptimized:
		resp.EBSOptimized = &attributeBooleanValueXML{Value: val == formTrue}
	case attrInstanceType:
		resp.InstanceType = &attributeValueXML{Value: val}
	case attrUserData:
		resp.UserData = &attributeValueXML{Value: val}
	}

	awsquery.WriteXMLResponse(w, resp)
}

// attachInstanceGroups populates resp.Groups from the instance's current
// security-group membership (with resolved names) for the groupSet attribute.
func (h *Handler) attachInstanceGroups(ctx context.Context, id string, resp *describeInstanceAttributeResponse) error {
	instances, err := h.compute.DescribeInstances(ctx, []string{id}, nil)
	if err != nil {
		return err
	}

	if len(instances) == 0 {
		return cerrors.Newf(cerrors.NotFound, "instance %q not found", id)
	}

	names := h.securityGroupNames(ctx, instances[0].SecurityGroups)
	for _, sg := range instances[0].SecurityGroups {
		resp.Groups = append(resp.Groups, groupItem{GroupID: sg, GroupName: names[sg]})
	}

	return nil
}

// attrForm maps an attribute element name (lowerCamel, as on the response) to
// the request parameter's PascalCase prefix used by ModifyInstanceAttribute.
func attrForm(name string) string {
	switch name {
	case attrDisableAPITermination:
		return "DisableApiTermination"
	case attrSourceDestCheck:
		return "SourceDestCheck"
	case attrEbsOptimized:
		return "EbsOptimized"
	case attrUserData:
		return "UserData"
	default:
		return name
	}
}

// getConsoleOutput handles Action=GetConsoleOutput. The console output is read
// from the real ComputeEngine backing the instance (empty for an instance with
// no engine wired). Real EC2 returns the output base64-encoded in <output>.
func (h *Handler) getConsoleOutput(w http.ResponseWriter, r *http.Request) {
	id := r.Form.Get("InstanceId")

	reader, ok := h.compute.(computedriver.ConsoleReader)
	if !ok {
		writeErr(w, cerrors.New(cerrors.Unimplemented, "console output is not supported"))
		return
	}

	out, err := reader.GetConsoleOutput(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}

	awsquery.WriteXMLResponse(w, getConsoleOutputResponse{
		Xmlns:      awsquery.Namespace,
		RequestID:  awsquery.RequestID,
		InstanceID: id,
		Timestamp:  formatTime(time.Now().UTC()),
		Output:     base64.StdEncoding.EncodeToString(out),
	})
}

// decodeUserData decodes the base64 UserData the wire carries. Real EC2 UserData
// is always base64-encoded; a value that does not decode is passed through raw
// so a client that sent plain text still gets its boot script.
func decodeUserData(s string) string {
	if s == "" {
		return ""
	}

	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil {
		return string(decoded)
	}

	return s
}

// instanceCount returns how many instances RunInstances should launch.
// Real EC2 launches MaxCount instances when capacity allows, falling back to
// fewer (but at least MinCount) when it doesn't. Our in-memory backend has
// unlimited capacity, so we always launch MaxCount. Missing MinCount defaults
// to 1 and missing MaxCount defaults to MinCount. MinCount must be >= 1 and
// MaxCount >= MinCount; otherwise EC2 rejects the call with InvalidParameterValue
// and launches nothing.
func instanceCount(minStr, maxStr string) (int, error) {
	minN := 1

	if minStr != "" {
		n, err := strconv.Atoi(minStr)
		if err != nil {
			return 0, cerrors.Newf(cerrors.InvalidArgument, "Invalid value '%s' for parameter minCount", minStr)
		}

		minN = n
	}

	maxN := minN

	if maxStr != "" {
		n, err := strconv.Atoi(maxStr)
		if err != nil {
			return 0, cerrors.Newf(cerrors.InvalidArgument, "Invalid value '%s' for parameter maxCount", maxStr)
		}

		maxN = n
	}

	if minN < 1 {
		return 0, cerrors.Newf(cerrors.InvalidArgument,
			"Invalid value '%d' for parameter minCount is invalid. minCount must be greater than 0", minN)
	}

	if maxN < minN {
		return 0, cerrors.Newf(cerrors.InvalidArgument,
			"Invalid value '%d' for parameter maxCount is invalid. maxCount must be greater than or equal to minCount %d", maxN, minN)
	}

	return maxN, nil
}

// mergeTagSpecs flattens tag-specifications whose ResourceType matches
// resource ("instance", "volume", etc.) into a single map.
func mergeTagSpecs(specs []awsquery.TagSpec, resource string) map[string]string {
	if len(specs) == 0 {
		return nil
	}

	out := make(map[string]string)

	for _, s := range specs {
		if s.ResourceType != "" && s.ResourceType != resource {
			continue
		}

		for k, v := range s.Tags {
			out[k] = v
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// copyTagMap returns a shallow copy of tags, or nil for an empty input, so each
// consumer owns its own tag map.
func copyTagMap(tags map[string]string) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	out := make(map[string]string, len(tags))
	for k, v := range tags {
		out[k] = v
	}

	return out
}

// toInstanceXMLs converts driver instances to their XML wire form, resolving
// security-group names once for the whole batch and reading the ENI, volume and
// elastic-IP stores once so each instance reflects its real attachments (the
// authoritative attachment state lives in those stores, not on the instance's
// own scalar fields).
func (h *Handler) toInstanceXMLs(ctx context.Context, instances []computedriver.Instance) []instanceXML {
	names := h.securityGroupNames(ctx, collectSecurityGroups(instances))

	enisByInstance := h.enisByInstance(ctx)
	volsByInstance := h.volumesByInstance(ctx)
	eipByInstance := h.eipByInstance(ctx)

	out := make([]instanceXML, 0, len(instances))

	for i := range instances {
		inst := &instances[i]
		out = append(out, instanceXMLFor(inst, names, enisByInstance[inst.ID], volsByInstance[inst.ID], eipByInstance[inst.ID]))
	}

	return out
}

// enisByInstance groups every ENI in the networking store by the instance it is
// attached to, so each instance's networkInterfaceSet reflects the real
// interfaces (primary eth0 plus any AttachNetworkInterface secondaries). A
// networking backend that does not model ENIs yields an empty map, and callers
// fall back to the synthesized primary interface.
func (h *Handler) enisByInstance(ctx context.Context) map[string][]netdriver.NetworkInterface {
	store, ok := h.networkInterfaces()
	if !ok {
		return nil
	}

	enis, err := store.DescribeNetworkInterfaces(ctx, nil)
	if err != nil {
		return nil
	}

	out := make(map[string][]netdriver.NetworkInterface)

	for i := range enis {
		if id := enis[i].InstanceID; id != "" {
			out[id] = append(out[id], enis[i])
		}
	}

	return out
}

// volumesByInstance groups every attached EBS volume by the instance it is
// attached to, so each instance's blockDeviceMapping reflects the volumes the
// volume store says are attached to it.
func (h *Handler) volumesByInstance(ctx context.Context) map[string][]computedriver.VolumeInfo {
	if h.compute == nil {
		return nil
	}

	vols, err := h.compute.DescribeVolumes(ctx, nil)
	if err != nil {
		return nil
	}

	out := make(map[string][]computedriver.VolumeInfo)

	for i := range vols {
		if id := vols[i].AttachedTo; id != "" {
			out[id] = append(out[id], vols[i])
		}
	}

	return out
}

// eipByInstance maps each instance to the elastic IP associated with it, so an
// instance reports the public IP of its EIP (instances never carry one on their
// own scalar fields).
func (h *Handler) eipByInstance(ctx context.Context) map[string]*netdriver.ElasticIP {
	if h.vpc == nil {
		return nil
	}

	eips, err := h.vpc.DescribeAddresses(ctx, nil)
	if err != nil {
		return nil
	}

	out := make(map[string]*netdriver.ElasticIP)

	for i := range eips {
		if id := eips[i].InstanceID; id != "" {
			out[id] = &eips[i]
		}
	}

	return out
}

// instanceXMLFor builds the wire shape for one instance, including the static
// facts and derived placement / DNS / primary-ENI fields real EC2 reports.
func instanceXMLFor(
	inst *computedriver.Instance, names map[string]string,
	enis []netdriver.NetworkInterface, vols []computedriver.VolumeInfo, eip *netdriver.ElasticIP,
) instanceXML {
	// An associated elastic IP is what gives an instance its public address;
	// instances never carry one on their own scalar fields, so the EIP store is
	// authoritative for the reported public IP / DNS name.
	publicIP := inst.PublicIP
	if eip != nil && eip.PublicIP != "" {
		publicIP = eip.PublicIP
	}

	xi := instanceXML{
		InstanceID:         inst.ID,
		ImageID:            inst.ImageID,
		InstanceType:       inst.InstanceType,
		State:              instanceState{Code: stateCode(inst.State), Name: inst.State},
		LaunchTime:         inst.LaunchTime,
		SubnetID:           inst.SubnetID,
		VPCID:              inst.VPCID,
		PrivateIP:          inst.PrivateIP,
		PublicIP:           publicIP,
		PrivateDNSName:     privateDNSName(inst.PrivateIP),
		PublicDNSName:      publicDNSName(publicIP),
		KeyName:            inst.KeyName,
		AmiLaunchIndex:     0,
		Architecture:       archX86,
		RootDeviceType:     rootDeviceTypeEBS,
		RootDeviceName:     rootDeviceNameXVDA,
		VirtualizationType: virtualizationHVM,
		Hypervisor:         hypervisorXen,
		Placement:          &placementXML{AvailabilityZone: instanceZone(inst), Tenancy: tenancyDefault},
		Monitoring:         &monitoringXML{State: monitoringState(inst.Monitoring)},
		MetadataOptions:    metadataOptionsXMLFor(&inst.MetadataOptions),
	}

	for _, sg := range inst.SecurityGroups {
		xi.Groups = append(xi.Groups, groupItem{GroupID: sg, GroupName: names[sg]})
	}

	xi.NetworkInterfaces = instanceENIs(inst, xi.Groups, enis)
	xi.BlockDeviceMappings = instanceBlockDevices(vols)

	for k, v := range inst.Tags {
		xi.Tags = append(xi.Tags, tagItem{Key: k, Value: v})
	}

	if inst.IamInstanceProfile != nil {
		xi.IamInstanceProfile = &iamInstanceProfileXML{
			ARN: inst.IamInstanceProfile.ARN,
			ID:  inst.IamInstanceProfile.ID,
		}
	}

	if inst.Operator != nil {
		xi.Operator = &operatorXML{
			Managed:         inst.Operator.Managed,
			Principal:       inst.Operator.Principal,
			HiddenByDefault: inst.Operator.HiddenByDefault,
		}
	}

	return xi
}

// metadataOptionsXMLFor renders an instance's IMDS configuration, filling in the
// EC2 defaults for any field an older stored instance left unset.
func metadataOptionsXMLFor(o *computedriver.MetadataOptions) *metadataOptionsXML {
	hopLimit := o.HTTPPutResponseHopLimit
	if hopLimit == 0 {
		hopLimit = 1
	}

	return &metadataOptionsXML{
		State:                   nonEmpty(o.State, "applied"),
		HTTPTokens:              nonEmpty(o.HTTPTokens, "optional"),
		HTTPPutResponseHopLimit: hopLimit,
		HTTPEndpoint:            nonEmpty(o.HTTPEndpoint, "enabled"),
		HTTPProtocolIPv6:        nonEmpty(o.HTTPProtocolIPv6, "disabled"),
		InstanceMetadataTags:    nonEmpty(o.InstanceMetadataTags, "disabled"),
	}
}

// instanceENIs renders the instance's network interfaces from the ENI store:
// every interface attached to the instance (its primary eth0 plus any
// AttachNetworkInterface secondaries), each carrying its real eni- id, MAC,
// device index, private IP, subnet and attachment id. Interfaces are ordered by
// device index so eth0 comes first.
//
// When the store holds no interface for the instance — a launch with no subnet,
// or a networking backend that does not model ENIs — it falls back to a single
// synthesized primary interface from the instance's own subnet/VPC/private-IP so
// those instances still describe an interface.
func instanceENIs(inst *computedriver.Instance, groups []groupItem, enis []netdriver.NetworkInterface) []instanceENIXML {
	if len(enis) == 0 {
		if eni := synthesizedPrimaryENI(inst, groups); eni != nil {
			return []instanceENIXML{*eni}
		}

		return nil
	}

	sorted := append([]netdriver.NetworkInterface(nil), enis...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].DeviceIndex < sorted[j].DeviceIndex })

	out := make([]instanceENIXML, 0, len(sorted))

	for i := range sorted {
		eni := &sorted[i]
		// The primary interface carries the instance's security groups; a secondary
		// ENI's own groups are not tracked on the attachment, so only eth0 reflects
		// the instance-level group set here.
		var itemGroups []groupItem
		if eni.DeviceIndex == primaryDeviceIndex {
			itemGroups = groups
		}

		out = append(out, instanceENIXML{
			NetworkInterfaceID: eni.ID,
			SubnetID:           eni.SubnetID,
			VPCID:              eni.VPCID,
			MacAddress:         eni.MacAddress,
			PrivateIP:          eni.PrivateIP,
			Status:             eni.Status,
			Groups:             itemGroups,
			Attachment: instanceENIAttachmentXML{
				AttachmentID: eni.AttachmentID,
				DeviceIndex:  eni.DeviceIndex,
				Status:       eniAttachedStatus,
			},
		})
	}

	return out
}

// synthesizedPrimaryENI builds a best-effort primary interface from the
// instance's own subnet/VPC/private-IP/security-groups, for instances the ENI
// store does not model. Returns nil when there is nothing to describe.
func synthesizedPrimaryENI(inst *computedriver.Instance, groups []groupItem) *instanceENIXML {
	if inst.SubnetID == "" && inst.VPCID == "" && inst.PrivateIP == "" {
		return nil
	}

	return &instanceENIXML{
		SubnetID:   inst.SubnetID,
		VPCID:      inst.VPCID,
		PrivateIP:  inst.PrivateIP,
		Groups:     groups,
		Attachment: instanceENIAttachmentXML{DeviceIndex: primaryDeviceIndex, Status: eniAttachedStatus},
	}
}

// instanceBlockDevices renders the EBS volumes attached to the instance as
// blockDeviceMapping items. The volume store is authoritative for the linkage
// (AttachVolume records AttachedTo/Device); the instance side only reflects it.
func instanceBlockDevices(vols []computedriver.VolumeInfo) []instanceBlockDeviceXML {
	if len(vols) == 0 {
		return nil
	}

	sorted := append([]computedriver.VolumeInfo(nil), vols...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Device < sorted[j].Device })

	out := make([]instanceBlockDeviceXML, 0, len(sorted))

	for i := range sorted {
		v := &sorted[i]
		out = append(out, instanceBlockDeviceXML{
			DeviceName: v.Device,
			EBS: instanceEBSXML{
				VolumeID:            v.ID,
				Status:              eniAttachedStatus,
				AttachTime:          v.CreatedAt,
				DeleteOnTermination: false,
			},
		})
	}

	return out
}

// instanceZone returns the instance's availability zone, defaulting to a stable
// zone when the provider does not track placement.
func instanceZone(inst *computedriver.Instance) string {
	if len(inst.Zones) > 0 && inst.Zones[0] != "" {
		return inst.Zones[0]
	}

	return defaultZone
}

// monitoringState maps a stored monitoring value to the wire enum, defaulting to
// "disabled" (basic monitoring) when unset.
func monitoringState(state string) string {
	if state == "" {
		return monitorStateDisabled
	}

	return state
}

// collectSecurityGroups returns the de-duplicated security-group ids across the
// batch so names are resolved in a single networking lookup.
func collectSecurityGroups(instances []computedriver.Instance) []string {
	seen := make(map[string]struct{})

	var ids []string

	for i := range instances {
		for _, sg := range instances[i].SecurityGroups {
			if _, ok := seen[sg]; !ok {
				seen[sg] = struct{}{}

				ids = append(ids, sg)
			}
		}
	}

	return ids
}

// securityGroupNames resolves security-group ids to their names via the
// networking driver. It returns an empty map when no networking driver is wired
// or the lookup fails, so name resolution is best-effort (ids still render).
func (h *Handler) securityGroupNames(ctx context.Context, ids []string) map[string]string {
	names := make(map[string]string)
	if h.vpc == nil || len(ids) == 0 {
		return names
	}

	groups, err := h.vpc.DescribeSecurityGroups(ctx, ids)
	if err != nil {
		return names
	}

	for i := range groups {
		names[groups[i].ID] = groups[i].Name
	}

	return names
}

// privateDNSName derives the EC2 internal DNS name from a private IPv4
// (10.0.0.5 -> ip-10-0-0-5.ec2.internal). Empty for an instance with no IP.
func privateDNSName(ip string) string {
	if ip == "" {
		return ""
	}

	return "ip-" + strings.ReplaceAll(ip, ".", "-") + internalDNSSuffix
}

// publicDNSName derives the public DNS name from a public IPv4
// (52.1.2.3 -> ec2-52-1-2-3.compute-1.amazonaws.com). Empty when no public IP.
func publicDNSName(ip string) string {
	if ip == "" {
		return ""
	}

	return "ec2-" + strings.ReplaceAll(ip, ".", "-") + ".compute-1.amazonaws.com"
}

// toDriverFilters converts parsed filters to the driver's filter shape.
func toDriverFilters(in []awsquery.Filter) []computedriver.DescribeFilter {
	if len(in) == 0 {
		return nil
	}

	out := make([]computedriver.DescribeFilter, 0, len(in))
	for _, f := range in {
		out = append(out, computedriver.DescribeFilter{Name: f.Name, Values: f.Values})
	}

	return out
}

// priorStates captures each instance's state before a lifecycle operation so
// the response can report the real previousState (rather than a hardcoded one).
// A describe error yields an empty map; callers fall back to a zero previous.
func (h *Handler) priorStates(ctx context.Context, ids []string) map[string]instanceState {
	out := make(map[string]instanceState, len(ids))

	instances, err := h.compute.DescribeInstances(ctx, ids, nil)
	if err != nil {
		return out
	}

	for i := range instances {
		out[instances[i].ID] = instanceState{
			Code: stateCode(instances[i].State), Name: instances[i].State,
		}
	}

	return out
}

// stateChangesFrom builds a transition record for each id, deriving
// previousState from the states captured before the operation ran.
func stateChangesFrom(ids []string, previous map[string]instanceState, current instanceState) []stateChangeXML {
	out := make([]stateChangeXML, 0, len(ids))
	for _, id := range ids {
		out = append(out, stateChangeXML{
			InstanceID:    id,
			CurrentState:  current,
			PreviousState: previous[id],
		})
	}

	return out
}

// stripInstancePrefix removes the "i-" prefix so we can reuse the body as a
// reservation suffix ("r-<body>").
func stripInstancePrefix(id string) string {
	const prefix = "i-"
	if len(id) > len(prefix) && id[:len(prefix)] == prefix {
		return id[len(prefix):]
	}

	return id
}
