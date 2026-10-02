package apimanagement

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"maps"
	"sort"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// Portal setting names (…/portalsettings/{name}).
const (
	PortalSignIn     = "signin"
	PortalSignUp     = "signup"
	PortalDelegation = "delegation"
)

// Tenant access names (…/tenant/{name}).
const (
	TenantAccessName    = "access"
	TenantGitAccessName = "gitAccess"
)

// Policy formats the service-level policy accepts and stores inline.
const (
	PolicyFormatXML    = "xml"
	PolicyFormatRawXML = "rawxml"
)

// delegationValidationKey is the secret key of the delegation settings; Azure
// returns it only from listSecrets, never from a GET.
const delegationValidationKey = "validationKey"

// ChildResource is a stored child resource of a service (an API, a product,
// the service policy or a portal setting): its name, its properties block as
// Azure returns it, and its etag, which changes on every write.
type ChildResource struct {
	Name       string          `json:"name"`
	Properties json.RawMessage `json:"properties"`
	Etag       string          `json:"etag"`
}

// TenantAccess is a tenant access information entity (access or gitAccess):
// whether direct management / git access is enabled, and its principal and
// keys, which only listSecrets reveals.
type TenantAccess struct {
	Name         string `json:"name"`
	Enabled      bool   `json:"enabled"`
	PrincipalID  string `json:"principalId"`
	PrimaryKey   string `json:"primaryKey"`
	SecondaryKey string `json:"secondaryKey"`
	Etag         string `json:"etag"`
}

// Children are the child resources held for one service.
type Children struct {
	APIs     map[string]*ChildResource `json:"apis,omitempty"`
	Products map[string]*ChildResource `json:"products,omitempty"`
	Policy   *ChildResource            `json:"policy,omitempty"`
	Portal   map[string]*ChildResource `json:"portalSettings,omitempty"`
	Tenant   map[string]*TenantAccess  `json:"tenantAccess,omitempty"`
}

// ErrTierNotSupported reports a child resource that does not exist in the
// service's tier (the developer portal settings and tenant access on
// Consumption and v2).
var ErrTierNotSupported = errors.New("not supported in this API Management tier")

// hasDeveloperPortal reports whether the tier has the classic developer portal
// and tenant access surface: every tier but Consumption and the v2 tiers.
func hasDeveloperPortal(sku string) bool {
	return sku != skuConsumption && !strings.HasSuffix(strings.ToUpper(sku), "V2")
}

// productPublished is the state of a product visible on the developer portal.
const productPublished = "published"

// enabledFlag is the {"enabled": bool} block the portal settings share.
type enabledFlag struct {
	Enabled bool `json:"enabled"`
}

// apiSeed is the sample Echo API's properties block.
type apiSeed struct {
	DisplayName          string            `json:"displayName"`
	APIRevision          string            `json:"apiRevision"`
	Description          string            `json:"description"`
	SubscriptionRequired bool              `json:"subscriptionRequired"`
	ServiceURL           string            `json:"serviceUrl"`
	Path                 string            `json:"path"`
	Protocols            []string          `json:"protocols"`
	IsCurrent            bool              `json:"isCurrent"`
	KeyParameterNames    map[string]string `json:"subscriptionKeyParameterNames"`
}

// productSeed is a sample product's properties block.
type productSeed struct {
	DisplayName          string `json:"displayName"`
	Description          string `json:"description"`
	SubscriptionRequired bool   `json:"subscriptionRequired"`
	ApprovalRequired     bool   `json:"approvalRequired"`
	SubscriptionsLimit   int    `json:"subscriptionsLimit"`
	State                string `json:"state"`
}

// signUpSeed / delegationSeed are the default sign-up and delegation settings.
type signUpSeed struct {
	Enabled        bool `json:"enabled"`
	TermsOfService struct {
		Enabled         bool   `json:"enabled"`
		ConsentRequired bool   `json:"consentRequired"`
		Text            string `json:"text"`
	} `json:"termsOfService"`
}

type delegationSeed struct {
	URL              string      `json:"url"`
	Subscriptions    enabledFlag `json:"subscriptions"`
	UserRegistration enabledFlag `json:"userRegistration"`
}

// seedChildren is what Azure provisions with a new service: every tier but
// Consumption gets the sample Echo API and the Starter and Unlimited products,
// and the tiers with a developer portal get its default settings and the tenant
// access entities.
func seedChildren(s *Service) *Children {
	c := defaultSettings(s)

	if s.SkuName == skuConsumption {
		return c
	}

	c.APIs = map[string]*ChildResource{
		"echo-api": newChild(s, "apis", "echo-api", apiSeed{
			DisplayName:          "Echo API",
			APIRevision:          "1",
			SubscriptionRequired: true,
			ServiceURL:           "http://echoapi.cloudapp.net/api",
			Path:                 "echo",
			Protocols:            []string{"https"},
			IsCurrent:            true,
			KeyParameterNames:    map[string]string{"header": "Ocp-Apim-Subscription-Key", "query": "subscription-key"},
		}),
	}

	c.Products = map[string]*ChildResource{
		"starter": newChild(s, "products", "starter", productSeed{
			DisplayName:          "Starter",
			Description:          "Subscribers will be able to run 5 calls/minute up to a maximum of 100 calls/week.",
			SubscriptionRequired: true,
			SubscriptionsLimit:   1,
			State:                productPublished,
		}),
		"unlimited": newChild(s, "products", "unlimited", productSeed{
			DisplayName:          "Unlimited",
			Description:          "Subscribers have completely unlimited access to the API. Administrator approval is required.",
			SubscriptionRequired: true,
			ApprovalRequired:     true,
			SubscriptionsLimit:   1,
			State:                productPublished,
		}),
	}

	return c
}

// defaultSettings is the developer portal settings and tenant access a service
// starts with (none on the tiers without a developer portal).
func defaultSettings(s *Service) *Children {
	c := &Children{}
	if !hasDeveloperPortal(s.SkuName) {
		return c
	}

	c.Portal = map[string]*ChildResource{
		PortalSignIn:     newChild(s, "portalsettings", PortalSignIn, enabledFlag{}),
		PortalSignUp:     newChild(s, "portalsettings", PortalSignUp, signUpSeed{Enabled: true}),
		PortalDelegation: newChild(s, "portalsettings", PortalDelegation, delegationSeed{}),
	}

	principals := map[string]string{TenantAccessName: "integration", TenantGitAccessName: "git"}
	c.Tenant = map[string]*TenantAccess{}

	for n, principal := range principals {
		seed := "apimanagement/tenant/" + serviceKey(s.Subscription, s.ResourceGroup, s.Name) + "/" + n
		c.Tenant[strings.ToLower(n)] = &TenantAccess{
			Name:         n,
			PrincipalID:  principal,
			PrimaryKey:   secretKey(seed + "/primary"),
			SecondaryKey: secretKey(seed + "/secondary"),
			Etag:         idgen.SyntheticGUID(seed + "/etag"),
		}
	}

	return c
}

// secretKey renders a deterministic base64 access key.
func secretKey(seed string) string {
	return base64.StdEncoding.EncodeToString([]byte(idgen.SyntheticGUID(seed) + idgen.SyntheticGUID(seed+"/2")))
}

// newChild builds a seeded child resource with its first etag.
func newChild(s *Service, kind, name string, props any) *ChildResource {
	raw, err := json.Marshal(props)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}

	return &ChildResource{
		Name:       name,
		Properties: raw,
		Etag:       idgen.SyntheticGUID("apimanagement/" + serviceKey(s.Subscription, s.ResourceGroup, s.Name) + "/" + kind + "/" + name),
	}
}

// rotate gives a child resource a new etag after a write.
func (c *ChildResource) rotate() {
	c.Etag = idgen.SyntheticGUID("apimanagement/child/" + c.Name + "/" + c.Etag + "/" + string(c.Properties))
}

// cloneChild deep-copies a child resource.
func cloneChild(c *ChildResource) ChildResource {
	out := *c
	out.Properties = append(json.RawMessage(nil), c.Properties...)

	return out
}

// childrenLocked returns the live service at sub/rg/name and its children,
// creating the default settings for a service restored from a snapshot taken
// before child resources existed. The caller holds m.mu for writing.
func (m *Mock) childrenLocked(sub, rg, name string) (*Service, *Children, error) {
	k := serviceKey(sub, rg, name)

	s, ok := m.services.Get(k)
	if !ok {
		return nil, nil, notFound(name)
	}

	c, ok := m.children.Get(k)
	if !ok {
		c = defaultSettings(s)
		m.children.Set(k, c)
	}

	return s, c, nil
}

// childNotFound is the NotFound error for a missing child resource.
func childNotFound(kind, name string) error {
	return cerrors.Newf(cerrors.NotFound, "%s %q not found", kind, name)
}

// childIfMatch enforces a conditional child write or delete.
func childIfMatch(etag, ifMatch, kind, name string) error {
	if ifMatch == "" || ifMatch == wildcardETag || etagMatches(etag, ifMatch) {
		return nil
	}

	return cerrors.Newf(cerrors.FailedPrecondition,
		"the If-Match etag %s does not match the current state of %s %q", ifMatch, kind, name)
}

// collection selects one of the named child collections.
func collection(c *Children, kind string) map[string]*ChildResource {
	if kind == "apis" {
		return c.APIs
	}

	return c.Products
}

// listChildren returns a child collection sorted by name.
func (m *Mock) listChildren(sub, rg, svc, kind string) ([]ChildResource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, c, err := m.childrenLocked(sub, rg, svc)
	if err != nil {
		return nil, err
	}

	out := make([]ChildResource, 0, len(collection(c, kind)))
	for _, r := range collection(c, kind) {
		out = append(out, cloneChild(r))
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// getChild returns one member of a child collection.
func (m *Mock) getChild(sub, rg, svc, kind, name string) (ChildResource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, c, err := m.childrenLocked(sub, rg, svc)
	if err != nil {
		return ChildResource{}, err
	}

	r, ok := collection(c, kind)[strings.ToLower(name)]
	if !ok {
		return ChildResource{}, childNotFound(kind, name)
	}

	return cloneChild(r), nil
}

// deleteChild removes one member of a child collection, reporting whether it
// existed.
func (m *Mock) deleteChild(sub, rg, svc, kind, name, ifMatch string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, c, err := m.childrenLocked(sub, rg, svc)
	if err != nil {
		return false, err
	}

	col := collection(c, kind)

	r, ok := col[strings.ToLower(name)]
	if !ok {
		return false, nil
	}

	if err := childIfMatch(r.Etag, ifMatch, kind, name); err != nil {
		return false, err
	}

	delete(col, strings.ToLower(name))

	return true, nil
}

// ListAPIs returns the service's APIs, sorted by id.
func (m *Mock) ListAPIs(_ context.Context, sub, rg, svc string) ([]ChildResource, error) {
	return m.listChildren(sub, rg, svc, "apis")
}

// GetAPI returns one API of the service.
func (m *Mock) GetAPI(_ context.Context, sub, rg, svc, apiID string) (ChildResource, error) {
	return m.getChild(sub, rg, svc, "apis", apiID)
}

// DeleteAPI removes an API, reporting whether it existed.
func (m *Mock) DeleteAPI(_ context.Context, sub, rg, svc, apiID, ifMatch string) (bool, error) {
	return m.deleteChild(sub, rg, svc, "apis", apiID, ifMatch)
}

// ListProducts returns the service's products, sorted by id.
func (m *Mock) ListProducts(_ context.Context, sub, rg, svc string) ([]ChildResource, error) {
	return m.listChildren(sub, rg, svc, "products")
}

// GetProduct returns one product of the service.
func (m *Mock) GetProduct(_ context.Context, sub, rg, svc, productID string) (ChildResource, error) {
	return m.getChild(sub, rg, svc, "products", productID)
}

// DeleteProduct removes a product, reporting whether it existed.
func (m *Mock) DeleteProduct(_ context.Context, sub, rg, svc, productID, ifMatch string) (bool, error) {
	return m.deleteChild(sub, rg, svc, "products", productID, ifMatch)
}

// GetPolicy returns the service-level (global) policy, or NotFound when none is
// set.
func (m *Mock) GetPolicy(_ context.Context, sub, rg, svc string) (ChildResource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, c, err := m.childrenLocked(sub, rg, svc)
	if err != nil {
		return ChildResource{}, err
	}

	if c.Policy == nil {
		return ChildResource{}, childNotFound("policy", "policy")
	}

	return cloneChild(c.Policy), nil
}

// PutPolicy sets the service-level policy from an inline XML document (format
// xml or rawxml; the -link formats, which make Azure fetch the document, are
// not supported). The document must be well-formed XML. It reports whether the
// policy was newly created.
func (m *Mock) PutPolicy(_ context.Context, sub, rg, svc, value, format, ifMatch string) (ChildResource, bool, error) {
	format, err := validatePolicy(value, format)
	if err != nil {
		return ChildResource{}, false, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	_, c, err := m.childrenLocked(sub, rg, svc)
	if err != nil {
		return ChildResource{}, false, err
	}

	created := c.Policy == nil

	current := ""
	if !created {
		current = c.Policy.Etag
	}

	// A conditional write on a policy that does not exist yet fails: no etag
	// can match it.
	if mErr := childIfMatch(current, ifMatch, "policy", "policy"); mErr != nil {
		return ChildResource{}, false, mErr
	}

	if created {
		c.Policy = &ChildResource{Name: "policy"}
	}

	raw, err := json.Marshal(map[string]string{"value": value, "format": format})
	if err != nil {
		return ChildResource{}, false, cerrors.Newf(cerrors.Internal, "encode policy: %v", err)
	}

	c.Policy.Properties = raw
	c.Policy.rotate()

	return cloneChild(c.Policy), created, nil
}

// validatePolicy checks the format (defaulting to xml) and the document, and
// returns the format to store.
func validatePolicy(value, format string) (string, error) {
	if format == "" {
		format = PolicyFormatXML
	}

	if format != PolicyFormatXML && format != PolicyFormatRawXML {
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"policy format %q is not supported: use %q or %q", format, PolicyFormatXML, PolicyFormatRawXML)
	}

	return format, wellFormedXML(value)
}

// wellFormedXML rejects a policy document that is empty or not well-formed.
func wellFormedXML(doc string) error {
	if strings.TrimSpace(doc) == "" {
		return cerrors.New(cerrors.InvalidArgument, "policy value is required")
	}

	dec := xml.NewDecoder(strings.NewReader(doc))

	for {
		_, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return cerrors.Newf(cerrors.InvalidArgument, "policy value is not well-formed XML: %v", err)
		}
	}
}

// DeletePolicy removes the service-level policy, reporting whether one was set.
func (m *Mock) DeletePolicy(_ context.Context, sub, rg, svc, ifMatch string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, c, err := m.childrenLocked(sub, rg, svc)
	if err != nil {
		return false, err
	}

	if c.Policy == nil {
		return false, nil
	}

	if err := childIfMatch(c.Policy.Etag, ifMatch, "policy", "policy"); err != nil {
		return false, err
	}

	c.Policy = nil

	return true, nil
}

// portalLocked returns the named portal setting of a service whose tier has a
// developer portal. The caller holds m.mu.
func (m *Mock) portalLocked(sub, rg, svc, name string) (*ChildResource, error) {
	s, c, err := m.childrenLocked(sub, rg, svc)
	if err != nil {
		return nil, err
	}

	if !hasDeveloperPortal(s.SkuName) {
		return nil, coded(ErrTierNotSupported, cerrors.Newf(cerrors.InvalidArgument,
			"portal settings are not supported in the %s tier", s.SkuName))
	}

	r, ok := c.Portal[strings.ToLower(name)]
	if !ok {
		return nil, childNotFound("portal setting", name)
	}

	return r, nil
}

// GetPortalSetting returns a developer portal setting (signin, signup or
// delegation). The delegation validation key is a secret and is left out; read
// it with DelegationValidationKey.
func (m *Mock) GetPortalSetting(_ context.Context, sub, rg, svc, name string) (ChildResource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, err := m.portalLocked(sub, rg, svc, name)
	if err != nil {
		return ChildResource{}, err
	}

	out := cloneChild(r)
	out.Properties = withoutKey(out.Properties, delegationValidationKey)

	return out, nil
}

// PutPortalSetting replaces a developer portal setting's properties block.
func (m *Mock) PutPortalSetting(
	_ context.Context, sub, rg, svc, name string, props json.RawMessage, ifMatch string,
) (ChildResource, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(props, &obj); err != nil || obj == nil {
		return ChildResource{}, cerrors.New(cerrors.InvalidArgument, "properties must be a JSON object")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	r, err := m.portalLocked(sub, rg, svc, name)
	if err != nil {
		return ChildResource{}, err
	}

	if err := childIfMatch(r.Etag, ifMatch, "portal setting", name); err != nil {
		return ChildResource{}, err
	}

	r.Properties = append(json.RawMessage(nil), props...)
	r.rotate()

	out := cloneChild(r)
	out.Properties = withoutKey(out.Properties, delegationValidationKey)

	return out, nil
}

// DelegationValidationKey returns the delegation settings' validation key (the
// delegation listSecrets action), "" when none is set.
func (m *Mock) DelegationValidationKey(_ context.Context, sub, rg, svc string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, err := m.portalLocked(sub, rg, svc, PortalDelegation)
	if err != nil {
		return "", err
	}

	var p struct {
		ValidationKey string `json:"validationKey"`
	}

	_ = json.Unmarshal(r.Properties, &p)

	return p.ValidationKey, nil
}

// withoutKey drops one top-level key from a JSON object.
func withoutKey(raw json.RawMessage, key string) json.RawMessage {
	obj := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw
	}

	if _, ok := obj[key]; !ok {
		return raw
	}

	delete(obj, key)

	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}

	return out
}

// tenantLocked returns a tenant access entity of a service whose tier supports
// it. The caller holds m.mu.
func (m *Mock) tenantLocked(sub, rg, svc, name string) (*TenantAccess, error) {
	s, c, err := m.childrenLocked(sub, rg, svc)
	if err != nil {
		return nil, err
	}

	if !hasDeveloperPortal(s.SkuName) {
		return nil, coded(ErrTierNotSupported, cerrors.Newf(cerrors.InvalidArgument,
			"tenant access is not supported in the %s tier", s.SkuName))
	}

	t, ok := c.Tenant[strings.ToLower(name)]
	if !ok {
		return nil, childNotFound("tenant access", name)
	}

	return t, nil
}

// GetTenantAccess returns a tenant access entity (access or gitAccess),
// including its keys; the HTTP layer reveals them only from listSecrets.
func (m *Mock) GetTenantAccess(_ context.Context, sub, rg, svc, name string) (TenantAccess, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.tenantLocked(sub, rg, svc, name)
	if err != nil {
		return TenantAccess{}, err
	}

	return *t, nil
}

// UpdateTenantAccess enables or disables a tenant access entity.
func (m *Mock) UpdateTenantAccess(
	_ context.Context, sub, rg, svc, name string, enabled *bool, ifMatch string,
) (TenantAccess, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.tenantLocked(sub, rg, svc, name)
	if err != nil {
		return TenantAccess{}, err
	}

	if err := childIfMatch(t.Etag, ifMatch, "tenant access", name); err != nil {
		return TenantAccess{}, err
	}

	if enabled != nil {
		t.Enabled = *enabled
	}

	t.Etag = idgen.SyntheticGUID("apimanagement/tenant/" + t.Name + "/" + t.Etag)

	return *t, nil
}

// cloneChildren deep-copies a children set (for snapshots and soft delete).
func cloneChildren(c *Children) *Children {
	if c == nil {
		return nil
	}

	out := &Children{
		APIs:     cloneChildMap(c.APIs),
		Products: cloneChildMap(c.Products),
		Portal:   cloneChildMap(c.Portal),
		Tenant:   maps.Clone(c.Tenant),
	}

	if c.Policy != nil {
		p := cloneChild(c.Policy)
		out.Policy = &p
	}

	for k, t := range out.Tenant {
		cp := *t
		out.Tenant[k] = &cp
	}

	return out
}

// cloneChildMap deep-copies a child collection.
func cloneChildMap(in map[string]*ChildResource) map[string]*ChildResource {
	if in == nil {
		return nil
	}

	out := make(map[string]*ChildResource, len(in))

	for k, v := range in {
		c := cloneChild(v)
		out[k] = &c
	}

	return out
}
