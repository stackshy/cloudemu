package sagemaker

import (
	"context"
	"net"
	"regexp"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/sagemaker/driver"
)

const (
	maxWorkteamDescLen = 200
	minCIDRLen         = 4
	maxCIDRLen         = 64
)

var (
	workteamNamePattern  = regexp.MustCompile(`^[a-zA-Z0-9](-*[a-zA-Z0-9]){0,62}$`) //nolint:gochecknoglobals // compiled once
	workforceNamePattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]){0,62}$`) //nolint:gochecknoglobals // compiled once
)

func (m *Mock) workforceARN(name string) string { return m.arn("workforce/" + name) }

func (m *Mock) workteamARN(name string) string { return m.arn("workteam/private-crowd/" + name) }

// --- Workforce ---

func cloneWorkforce(in *driver.Workforce) *driver.Workforce {
	out := *in
	out.SourceCIDRs = append([]string(nil), in.SourceCIDRs...)

	if in.Cognito != nil {
		c := *in.Cognito
		out.Cognito = &c
	}

	if in.OIDC != nil {
		o := *in.OIDC
		o.ExtraParams = copyStrMap(in.OIDC.ExtraParams)
		out.OIDC = &o
	}

	if in.VPC != nil {
		v := *in.VPC
		v.SecurityGroupIDs = append([]string(nil), in.VPC.SecurityGroupIDs...)
		v.Subnets = append([]string(nil), in.VPC.Subnets...)
		out.VPC = &v
	}

	return &out
}

// observeWorkforce overlays an in-flight --async-settle window.
func (m *Mock) observeWorkforce(w *driver.Workforce) *driver.Workforce {
	out := cloneWorkforce(w)
	out.Status = m.wfSettle.State(w.Name, m.opts.Clock.Now(), w.Status)

	return out
}

func validateOIDC(o *driver.OIDCConfig) error {
	for field, v := range map[string]string{
		"authorizationEndpoint": o.AuthorizationEndpoint, "clientId": o.ClientID, "clientSecret": o.ClientSecret,
		"issuer": o.Issuer, "jwksUri": o.JwksURI, "logoutEndpoint": o.LogoutEndpoint,
		"tokenEndpoint": o.TokenEndpoint, "userInfoEndpoint": o.UserInfoEndpoint,
	} {
		if v == "" {
			return errors.Newf(errors.InvalidArgument, "oidcConfig.%s is required", field)
		}
	}

	for _, u := range []string{o.AuthorizationEndpoint, o.Issuer, o.JwksURI, o.LogoutEndpoint, o.TokenEndpoint, o.UserInfoEndpoint} {
		if !strings.HasPrefix(u, "https://") {
			return errors.Newf(errors.InvalidArgument, "oidcConfig endpoint %q must be an https URL", u)
		}
	}

	return nil
}

// validateCIDRs checks SourceIpConfig.Cidrs: 1-10 entries, each a valid CIDR of
// 4-64 characters; an IPv6 CIDR requires the dualstack address type.
func validateCIDRs(cidrs []string, ipType string) error {
	if len(cidrs) == 0 || len(cidrs) > driver.MaxWorkforceCIDRs {
		return errors.Newf(errors.InvalidArgument, "sourceIpConfig.cidrs must contain between 1 and %d items", driver.MaxWorkforceCIDRs)
	}

	for _, c := range cidrs {
		if len(c) < minCIDRLen || len(c) > maxCIDRLen {
			return errors.Newf(errors.InvalidArgument, "cidr %q must be %d-%d characters", c, minCIDRLen, maxCIDRLen)
		}

		ip, _, err := net.ParseCIDR(c)
		if err != nil {
			return errors.Newf(errors.InvalidArgument, "cidr %q is not a valid CIDR block", c)
		}

		if ip.To4() == nil && ipType != "dualstack" {
			return errors.Newf(errors.InvalidArgument, "cidr %q is IPv6 and needs ipAddressType dualstack", c)
		}
	}

	return nil
}

func validateIPType(t string) error {
	if t != "" && t != "ipv4" && t != "dualstack" {
		return errors.Newf(errors.InvalidArgument, "ipAddressType must be ipv4 or dualstack, not %q", t)
	}

	return nil
}

func validateWorkforceSpec(spec *driver.WorkforceSpec) error {
	if !workforceNamePattern.MatchString(spec.Name) {
		return errors.Newf(errors.InvalidArgument, "workforceName %q must be 1-63 letters, digits and hyphens", spec.Name)
	}

	if (spec.Cognito == nil) == (spec.OIDC == nil) {
		return errors.New(errors.InvalidArgument, "specify exactly one of cognitoConfig and oidcConfig")
	}

	if spec.Cognito != nil && (spec.Cognito.ClientID == "" || spec.Cognito.UserPool == "") {
		return errors.New(errors.InvalidArgument, "cognitoConfig.clientId and cognitoConfig.userPool are required")
	}

	if spec.OIDC != nil {
		if err := validateOIDC(spec.OIDC); err != nil {
			return err
		}
	}

	if err := validateIPType(spec.IPAddressType); err != nil {
		return err
	}

	if len(spec.SourceCIDRs) > 0 {
		if spec.VPC != nil {
			return errors.New(errors.InvalidArgument, "sourceIpConfig is not supported for a workforce in a VPC")
		}

		if err := validateCIDRs(spec.SourceCIDRs, spec.IPAddressType); err != nil {
			return err
		}
	}

	return validateTags(spec.Tags)
}

// CreateWorkforce creates the account's private workforce. Only one workforce
// exists per region and account.
func (m *Mock) CreateWorkforce(_ context.Context, spec driver.WorkforceSpec) (*driver.Workforce, error) {
	if err := validateWorkforceSpec(&spec); err != nil {
		return nil, err
	}

	m.wfMu.Lock()
	defer m.wfMu.Unlock()

	if m.workforces.Len() > 0 {
		return nil, errors.New(errors.AlreadyExists, "A workforce already exists in this region; delete it before creating another")
	}

	now := m.now()
	arn := m.workforceARN(spec.Name)
	w := &driver.Workforce{
		Name:            spec.Name,
		ARN:             arn,
		Status:          driver.WorkforceActive,
		SubDomain:       spec.Name + ".labeling." + m.opts.Region + ".sagemaker.aws",
		Cognito:         spec.Cognito,
		OIDC:            spec.OIDC,
		SourceCIDRs:     spec.SourceCIDRs,
		IPAddressType:   orDefault(spec.IPAddressType, "ipv4"),
		VPC:             spec.VPC,
		CreateDate:      now,
		LastUpdatedDate: now,
	}
	w = cloneWorkforce(w)

	if !m.workforces.SetIfAbsent(spec.Name, w) {
		return nil, errors.Newf(errors.AlreadyExists, "workforce %q already exists", spec.Name)
	}

	m.setTags(arn, spec.Tags)
	m.wfSettle.Begin(spec.Name, driver.WorkforceInitializing, m.opts.Clock.Now(), m.opts.SettleDuration(settle.DefaultProjectSettle))

	return m.observeWorkforce(w), nil
}

func (m *Mock) DescribeWorkforce(_ context.Context, name string) (*driver.Workforce, error) {
	w, ok := m.workforces.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "workforce %q not found", name)
	}

	return m.observeWorkforce(w), nil
}

func (m *Mock) ListWorkforces(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.Workforce], error) {
	return listObserved(m.workforces, m.observeWorkforce, listSpec[driver.Workforce]{
		name:    func(t *driver.Workforce) string { return t.Name },
		created: func(t *driver.Workforce) string { return t.CreateDate },
	}, opts)
}

// UpdateWorkforce changes a workforce's source IP restriction, OIDC provider,
// address type or VPC configuration. The OIDC configuration cannot change while
// workteams exist (ConflictException).
func (m *Mock) UpdateWorkforce(_ context.Context, up driver.WorkforceUpdate) (*driver.Workforce, error) {
	m.wfMu.Lock()
	defer m.wfMu.Unlock()

	w, ok := m.workforces.Get(up.Name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "workforce %q not found", up.Name)
	}

	updated := cloneWorkforce(w)

	if up.OIDC != nil {
		if w.OIDC == nil {
			return nil, errors.New(errors.InvalidArgument, "oidcConfig applies only to a workforce created with an OIDC identity provider")
		}

		if err := validateOIDC(up.OIDC); err != nil {
			return nil, err
		}

		if m.workteams.Len() > 0 {
			return nil, driver.Conflict("The OIDC configuration cannot be updated while the workforce has workteams")
		}

		o := *up.OIDC
		updated.OIDC = &o
	}

	if up.IPAddressType != nil {
		if err := validateIPType(*up.IPAddressType); err != nil {
			return nil, err
		}

		updated.IPAddressType = *up.IPAddressType
	}

	if up.VPC != nil {
		v := *up.VPC
		updated.VPC = &v
	}

	if up.SourceCIDRs != nil {
		if updated.VPC != nil {
			return nil, errors.New(errors.InvalidArgument, "sourceIpConfig is not supported for a workforce in a VPC")
		}

		if err := validateCIDRs(up.SourceCIDRs, updated.IPAddressType); err != nil {
			return nil, err
		}

		updated.SourceCIDRs = append([]string(nil), up.SourceCIDRs...)
	}

	updated.Status = driver.WorkforceActive
	updated.LastUpdatedDate = m.now()
	m.workforces.Set(up.Name, updated)
	m.wfSettle.Begin(up.Name, driver.WorkforceUpdating, m.opts.Clock.Now(), m.opts.SettleDuration(settle.DefaultProjectSettle))

	return m.observeWorkforce(updated), nil
}

// DeleteWorkforce deletes the workforce. A workforce that still has workteams is
// ResourceInUse.
func (m *Mock) DeleteWorkforce(_ context.Context, name string) error {
	m.wfMu.Lock()
	defer m.wfMu.Unlock()

	w, ok := m.workforces.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "workforce %q not found", name)
	}

	for _, t := range m.workteams.All() {
		if t.WorkforceName == name {
			return driver.InUse("Workforce %q still has workteams; delete them first", name)
		}
	}

	m.workforces.Delete(name)
	m.wfSettle.Clear(name)
	m.dropTags(w.ARN)

	return nil
}

// --- Workteam ---

func cloneWorkteam(in *driver.Workteam) *driver.Workteam {
	out := *in
	out.Members = cloneMembers(in.Members)
	out.ProductListingIDs = append([]string(nil), in.ProductListingIDs...)

	return &out
}

func cloneMembers(in []driver.MemberDefinition) []driver.MemberDefinition {
	if in == nil {
		return nil
	}

	out := make([]driver.MemberDefinition, len(in))

	for i, d := range in {
		d.OIDCGroups = append([]string(nil), d.OIDCGroups...)
		out[i] = d
	}

	return out
}

// validateMembers checks a workteam's member definitions against the workforce
// they belong to.
func validateMembers(members []driver.MemberDefinition, wf *driver.Workforce) error {
	if len(members) == 0 || len(members) > driver.MaxWorkteamMembers {
		return errors.Newf(errors.InvalidArgument, "memberDefinitions must contain between 1 and %d items", driver.MaxWorkteamMembers)
	}

	for i := range members {
		d := &members[i]
		cognito := d.CognitoClientID != "" || d.CognitoUserPool != "" || d.CognitoUserGroup != ""
		oidc := len(d.OIDCGroups) > 0

		if cognito == oidc {
			return errors.New(errors.InvalidArgument, "each member definition must be exactly one of a Cognito user group and OIDC groups")
		}

		if cognito {
			if wf.Cognito == nil {
				return errors.New(errors.InvalidArgument, "Cognito member definitions need a workforce created with cognitoConfig")
			}

			if d.CognitoClientID != wf.Cognito.ClientID || d.CognitoUserPool != wf.Cognito.UserPool || d.CognitoUserGroup == "" {
				return errors.New(errors.InvalidArgument, "Cognito member definitions must use the workforce's user pool and client and name a user group")
			}

			continue
		}

		if wf.OIDC == nil {
			return errors.New(errors.InvalidArgument, "OIDC member definitions need a workforce created with oidcConfig")
		}

		for _, g := range d.OIDCGroups {
			if g == "" {
				return errors.New(errors.InvalidArgument, "oidcMemberDefinition.groups must not contain empty names")
			}
		}
	}

	return nil
}

func validatePresign(v string) error {
	if v != "" && v != "Enabled" && v != "Disabled" {
		return errors.Newf(errors.InvalidArgument, "workerAccessConfiguration source IP constraint must be Enabled or Disabled, not %q", v)
	}

	return nil
}

// CreateWorkteam creates a private workteam in a workforce. An account holds at
// most 25 workteams per region.
func (m *Mock) CreateWorkteam(_ context.Context, spec driver.WorkteamSpec) (*driver.Workteam, error) {
	if !workteamNamePattern.MatchString(spec.Name) {
		return nil, errors.Newf(errors.InvalidArgument, "workteamName %q must be 1-63 letters, digits and hyphens", spec.Name)
	}

	if spec.Description == "" || len(spec.Description) > maxWorkteamDescLen {
		return nil, errors.Newf(errors.InvalidArgument, "description must be 1-%d characters", maxWorkteamDescLen)
	}

	for _, v := range []string{spec.S3PresignSourceIP, spec.S3PresignVpcSourceIP} {
		if err := validatePresign(v); err != nil {
			return nil, err
		}
	}

	if err := validateTags(spec.Tags); err != nil {
		return nil, err
	}

	wfName := orDefault(spec.WorkforceName, driver.DefaultWorkforceName)

	m.wfMu.Lock()
	defer m.wfMu.Unlock()

	wf, ok := m.workforces.Get(wfName)
	if !ok {
		return nil, errors.Newf(errors.InvalidArgument, "Could not find workforce %q; create a workforce first", wfName)
	}

	if err := validateMembers(spec.Members, wf); err != nil {
		return nil, err
	}

	if m.workteams.Len() >= driver.MaxWorkteams {
		return nil, errors.Newf(errors.ResourceExhausted, "an account can have at most %d workteams per region", driver.MaxWorkteams)
	}

	now := m.now()
	arn := m.workteamARN(spec.Name)
	t := &driver.Workteam{
		Name:                 spec.Name,
		ARN:                  arn,
		Description:          spec.Description,
		WorkforceName:        wfName,
		WorkforceARN:         wf.ARN,
		SubDomain:            spec.Name + ".labeling." + m.opts.Region + ".sagemaker.aws",
		Members:              cloneMembers(spec.Members),
		NotificationTopicARN: spec.NotificationTopicARN,
		S3PresignSourceIP:    spec.S3PresignSourceIP,
		S3PresignVpcSourceIP: spec.S3PresignVpcSourceIP,
		CreateDate:           now,
		LastUpdatedDate:      now,
	}

	if !m.workteams.SetIfAbsent(spec.Name, t) {
		return nil, driver.InUse("workteam %q already exists", spec.Name)
	}

	m.setTags(arn, spec.Tags)

	return cloneWorkteam(t), nil
}

func (m *Mock) DescribeWorkteam(_ context.Context, name string) (*driver.Workteam, error) {
	t, ok := m.workteams.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "workteam %q not found", name)
	}

	return cloneWorkteam(t), nil
}

func (m *Mock) ListWorkteams(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.Workteam], error) {
	return listFrom(m.workteams, listSpec[driver.Workteam]{
		name:    func(t *driver.Workteam) string { return t.Name },
		created: func(t *driver.Workteam) string { return t.CreateDate },
		clone:   func(t *driver.Workteam) driver.Workteam { return *cloneWorkteam(t) },
	}, opts)
}

// UpdateWorkteam updates a workteam's description, members, notification topic
// or worker access configuration.
func (m *Mock) UpdateWorkteam(_ context.Context, up driver.WorkteamUpdate) (*driver.Workteam, error) {
	m.wfMu.Lock()
	defer m.wfMu.Unlock()

	t, ok := m.workteams.Get(up.Name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "workteam %q not found", up.Name)
	}

	updated := cloneWorkteam(t)

	if up.Description != nil {
		if *up.Description == "" || len(*up.Description) > maxWorkteamDescLen {
			return nil, errors.Newf(errors.InvalidArgument, "description must be 1-%d characters", maxWorkteamDescLen)
		}

		updated.Description = *up.Description
	}

	if up.Members != nil {
		wf, found := m.workforces.Get(t.WorkforceName)
		if !found {
			return nil, errors.Newf(errors.InvalidArgument, "workforce %q not found", t.WorkforceName)
		}

		if err := validateMembers(up.Members, wf); err != nil {
			return nil, err
		}

		updated.Members = cloneMembers(up.Members)
	}

	if up.NotificationTopicARN != nil {
		updated.NotificationTopicARN = *up.NotificationTopicARN
	}

	for _, p := range []struct {
		src *string
		dst *string
	}{{up.S3PresignSourceIP, &updated.S3PresignSourceIP}, {up.S3PresignVpcSourceIP, &updated.S3PresignVpcSourceIP}} {
		if p.src == nil {
			continue
		}

		if err := validatePresign(*p.src); err != nil {
			return nil, err
		}

		*p.dst = *p.src
	}

	updated.LastUpdatedDate = m.now()
	m.workteams.Set(up.Name, updated)

	return cloneWorkteam(updated), nil
}

// DeleteWorkteam deletes a workteam and reports whether it existed; a missing
// workteam is not an error.
func (m *Mock) DeleteWorkteam(_ context.Context, name string) (bool, error) {
	m.wfMu.Lock()
	defer m.wfMu.Unlock()

	t, ok := m.workteams.Get(name)
	if !ok {
		return false, nil
	}

	m.workteams.Delete(name)
	m.dropTags(t.ARN)

	return true, nil
}

// DescribeSubscribedWorkteam looks up a Marketplace vendor workteam. The
// emulator has no Marketplace, so none is ever subscribed.
func (m *Mock) DescribeSubscribedWorkteam(_ context.Context, arn string) (*driver.SubscribedWorkteam, error) {
	if err := validateARN("workteamArn", arn); err != nil {
		return nil, err
	}

	return nil, errors.Newf(errors.NotFound, "subscribed workteam %q not found", arn)
}

// ListSubscribedWorkteams lists Marketplace vendor workteams: always empty.
func (m *Mock) ListSubscribedWorkteams(_ context.Context, opts driver.ListOptions) (driver.ListPage[driver.SubscribedWorkteam], error) {
	if opts.MaxResults < 0 || opts.MaxResults > driver.MaxListResults {
		return driver.ListPage[driver.SubscribedWorkteam]{}, errors.New(errors.InvalidArgument, "maxResults must be between 1 and 100")
	}

	return driver.ListPage[driver.SubscribedWorkteam]{Items: []driver.SubscribedWorkteam{}}, nil
}

// ListLabelingJobsForWorkteam lists the labeling jobs assigned to a workteam.
func (m *Mock) ListLabelingJobsForWorkteam(
	_ context.Context, workteamARN string, opts driver.ListOptions,
) (driver.ListPage[driver.LabelingJobForWorkteam], error) {
	if !m.hasWorkteamARN(workteamARN) {
		return driver.ListPage[driver.LabelingJobForWorkteam]{}, errors.Newf(errors.NotFound, "workteam %q not found", workteamARN)
	}

	items := make([]*driver.LabelingJobForWorkteam, 0)

	for _, j := range m.labelingJobs.SortedValues() {
		if j.WorkteamARN != workteamARN {
			continue
		}

		obs := m.observeLabelingJob(j)
		items = append(items, &driver.LabelingJobForWorkteam{
			JobName:                j.JobName,
			JobReferenceCode:       driver.JobReferenceCode(j.JobName),
			WorkRequesterAccountID: m.opts.AccountID,
			NumberOfHumanWorkers:   1,
			CreationTime:           j.CreationTime,
			HumanLabeled:           obs.LabeledObjects,
			Total:                  obs.LabeledObjects,
		})
	}

	return listItems(items, listSpec[driver.LabelingJobForWorkteam]{
		name:    func(t *driver.LabelingJobForWorkteam) string { return t.JobName },
		created: func(t *driver.LabelingJobForWorkteam) string { return t.CreationTime },
		extra: func(t *driver.LabelingJobForWorkteam, f map[string]string) bool {
			return strings.Contains(t.JobReferenceCode, f["JobReferenceCodeContains"])
		},
	}, opts)
}

// hasWorkteamARN reports whether arn names an existing workteam.
func (m *Mock) hasWorkteamARN(arn string) bool {
	for _, t := range m.workteams.All() {
		if t.ARN == arn {
			return true
		}
	}

	return false
}
