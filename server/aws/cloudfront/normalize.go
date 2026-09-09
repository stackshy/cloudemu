package cloudfront

import (
	"bytes"
	"encoding/xml"
)

// Server-side default literals real CloudFront fills into a DistributionConfig
// when the caller omits them. Terraform's aws_cloudfront_distribution reader
// dereferences several of these blocks unconditionally (e.g. OriginGroups,
// DefaultCacheBehavior.AllowedMethods.CachedMethods, TrustedSigners/KeyGroups
// items), so a config that leaves them out crashes the provider. Normalizing the
// response to a complete config keeps unmodified SDK/CLI/Terraform callers happy.
const (
	priceClassAll      = "PriceClass_All"
	httpVersionDefault = "http2"
	noneValue          = "none"
	boolTrueValue      = "true"
	boolFalseValue     = "false"

	quantityZeroInner = "<Quantity>0</Quantity>"
	trustedOffInner   = "<Enabled>false</Enabled><Quantity>0</Quantity>"

	allowedQtyTwo         = "2"
	getHeadItemsInner     = "<Method>GET</Method><Method>HEAD</Method>"
	cachedMethodsAllInner = "<Quantity>2</Quantity><Items>" + getHeadItemsInner + "</Items>"

	loggingOffInner = "<Enabled>false</Enabled><IncludeCookies>false</IncludeCookies>" +
		"<Bucket></Bucket><Prefix></Prefix>"
	restrictionsNoneInner = "<GeoRestriction><RestrictionType>" + noneValue +
		"</RestrictionType><Quantity>0</Quantity></GeoRestriction>"
	viewerCertDefaultInner = "<CloudFrontDefaultCertificate>true</CloudFrontDefaultCertificate>" +
		"<MinimumProtocolVersion>TLSv1</MinimumProtocolVersion>"

	connAttemptsDefault = "3"
	connTimeoutDefault  = "10"
	minTTLZero          = "0"
	defaultTTLValue     = "86400"
	maxTTLValue         = "31536000"

	distConfigOpen  = "<DistributionConfig>"
	distConfigClose = "</DistributionConfig>"
)

// distConfigModel is a structural view of a <DistributionConfig> used only to
// fill the server-side default blocks real CloudFront always returns. Every
// block a caller might send is captured verbatim as *xmlRaw so its sub-tree
// round-trips byte-for-byte untouched; only wholly-absent blocks are defaulted.
// DefaultCacheBehavior and Origins are modeled a level deeper because some of
// their guaranteed defaults live inside them.
type distConfigModel struct {
	XMLName                      xml.Name       `xml:"DistributionConfig"`
	CallerReference              *xmlRaw        `xml:"CallerReference"`
	Aliases                      *xmlRaw        `xml:"Aliases"`
	DefaultRootObject            *xmlRaw        `xml:"DefaultRootObject"`
	Origins                      *originsModel  `xml:"Origins"`
	OriginGroups                 *xmlRaw        `xml:"OriginGroups"`
	DefaultCacheBehavior         *behaviorModel `xml:"DefaultCacheBehavior"`
	CacheBehaviors               *xmlRaw        `xml:"CacheBehaviors"`
	CustomErrorResponses         *xmlRaw        `xml:"CustomErrorResponses"`
	Comment                      *xmlRaw        `xml:"Comment"`
	Logging                      *xmlRaw        `xml:"Logging"`
	PriceClass                   *xmlRaw        `xml:"PriceClass"`
	Enabled                      *xmlRaw        `xml:"Enabled"`
	ViewerCertificate            *xmlRaw        `xml:"ViewerCertificate"`
	Restrictions                 *xmlRaw        `xml:"Restrictions"`
	WebACLID                     *xmlRaw        `xml:"WebACLId"`
	HTTPVersion                  *xmlRaw        `xml:"HttpVersion"`
	IsIPV6Enabled                *xmlRaw        `xml:"IsIPV6Enabled"`
	Staging                      *xmlRaw        `xml:"Staging"`
	ContinuousDeploymentPolicyID *xmlRaw        `xml:"ContinuousDeploymentPolicyId"`
	AnycastIPListID              *xmlRaw        `xml:"AnycastIpListId"`
}

// originsModel structurally models <Origins> so each <Origin> can gain the
// per-origin defaults (connection attempts/timeout, empty custom headers).
type originsModel struct {
	Quantity *xmlRaw      `xml:"Quantity"`
	Items    []originItem `xml:"Items>Origin"`
}

// originItem models a single <Origin>. The origin-type blocks (S3/Custom/VPC
// origin config, origin shield, custom headers) are preserved verbatim so the
// caller's exact shape survives; only the always-returned scalars are defaulted.
type originItem struct {
	ID                    *xmlRaw `xml:"Id"`
	DomainName            *xmlRaw `xml:"DomainName"`
	OriginPath            *xmlRaw `xml:"OriginPath"`
	CustomHeaders         *xmlRaw `xml:"CustomHeaders"`
	S3OriginConfig        *xmlRaw `xml:"S3OriginConfig"`
	CustomOriginConfig    *xmlRaw `xml:"CustomOriginConfig"`
	VPCOriginConfig       *xmlRaw `xml:"VpcOriginConfig"`
	ConnectionAttempts    *xmlRaw `xml:"ConnectionAttempts"`
	ConnectionTimeout     *xmlRaw `xml:"ConnectionTimeout"`
	OriginShield          *xmlRaw `xml:"OriginShield"`
	OriginAccessControlID *xmlRaw `xml:"OriginAccessControlId"`
}

// behaviorModel structurally models <DefaultCacheBehavior>. The blocks the
// Terraform reader dereferences unconditionally (TrustedSigners, TrustedKeyGroups,
// Lambda/Function associations, AllowedMethods.CachedMethods) are defaulted when
// absent; AllowedMethods and ForwardedValues are modeled deeper for the same
// reason. Everything else is preserved verbatim.
type behaviorModel struct {
	PathPattern                *xmlRaw               `xml:"PathPattern"`
	TargetOriginID             *xmlRaw               `xml:"TargetOriginId"`
	TrustedSigners             *xmlRaw               `xml:"TrustedSigners"`
	TrustedKeyGroups           *xmlRaw               `xml:"TrustedKeyGroups"`
	ViewerProtocolPolicy       *xmlRaw               `xml:"ViewerProtocolPolicy"`
	AllowedMethods             *allowedMethodsModel  `xml:"AllowedMethods"`
	SmoothStreaming            *xmlRaw               `xml:"SmoothStreaming"`
	Compress                   *xmlRaw               `xml:"Compress"`
	LambdaFunctionAssociations *xmlRaw               `xml:"LambdaFunctionAssociations"`
	FunctionAssociations       *xmlRaw               `xml:"FunctionAssociations"`
	FieldLevelEncryptionID     *xmlRaw               `xml:"FieldLevelEncryptionId"`
	RealtimeLogConfigARN       *xmlRaw               `xml:"RealtimeLogConfigArn"`
	CachePolicyID              *xmlRaw               `xml:"CachePolicyId"`
	OriginRequestPolicyID      *xmlRaw               `xml:"OriginRequestPolicyId"`
	ResponseHeadersPolicyID    *xmlRaw               `xml:"ResponseHeadersPolicyId"`
	ForwardedValues            *forwardedValuesModel `xml:"ForwardedValues"`
	MinTTL                     *xmlRaw               `xml:"MinTTL"`
	DefaultTTL                 *xmlRaw               `xml:"DefaultTTL"`
	MaxTTL                     *xmlRaw               `xml:"MaxTTL"`
	GrpcConfig                 *xmlRaw               `xml:"GrpcConfig"`
}

// allowedMethodsModel models <AllowedMethods>; its nested <CachedMethods> is
// dereferenced unconditionally by the Terraform reader.
type allowedMethodsModel struct {
	Quantity      *xmlRaw `xml:"Quantity"`
	Items         *xmlRaw `xml:"Items"`
	CachedMethods *xmlRaw `xml:"CachedMethods"`
}

// forwardedValuesModel models the legacy <ForwardedValues> block so its
// always-returned Headers/QueryStringCacheKeys/Cookies defaults can be filled.
type forwardedValuesModel struct {
	QueryString          *xmlRaw       `xml:"QueryString"`
	Cookies              *cookiesModel `xml:"Cookies"`
	Headers              *xmlRaw       `xml:"Headers"`
	QueryStringCacheKeys *xmlRaw       `xml:"QueryStringCacheKeys"`
}

// cookiesModel models <Cookies> so WhitelistedNames is always present.
type cookiesModel struct {
	Forward          *xmlRaw `xml:"Forward"`
	WhitelistedNames *xmlRaw `xml:"WhitelistedNames"`
}

// normalizeConfigXML returns the inner XML of a <DistributionConfig> with every
// server-side default block real CloudFront guarantees filled in, so unmodified
// SDK/CLI/Terraform readers that dereference those blocks don't fault. Caller
// values are preserved exactly — only wholly-absent blocks are added. On any
// parse or re-encode failure it returns the stored bytes unchanged so a read
// never fails.
func normalizeConfigXML(inner []byte) []byte {
	if len(bytes.TrimSpace(inner)) == 0 {
		return inner
	}

	wrapped := make([]byte, 0, len(inner)+len(distConfigOpen)+len(distConfigClose))
	wrapped = append(wrapped, distConfigOpen...)
	wrapped = append(wrapped, inner...)
	wrapped = append(wrapped, distConfigClose...)

	var m distConfigModel
	if err := xml.Unmarshal(wrapped, &m); err != nil {
		return inner
	}

	m.applyDefaults()

	out, err := xml.Marshal(&m)
	if err != nil {
		return inner
	}

	out = bytes.TrimPrefix(out, []byte(distConfigOpen))
	out = bytes.TrimSuffix(out, []byte(distConfigClose))

	return out
}

// setIfNil points *pp at a raw block carrying inner when it is currently absent.
func setIfNil(pp **xmlRaw, inner string) {
	if *pp == nil {
		*pp = &xmlRaw{Inner: []byte(inner)}
	}
}

// applyDefaults fills the top-level blocks real CloudFront always returns and
// delegates the origin/default-cache-behavior sub-blocks.
func (m *distConfigModel) applyDefaults() {
	setIfNil(&m.Aliases, quantityZeroInner)
	setIfNil(&m.OriginGroups, quantityZeroInner)
	setIfNil(&m.CacheBehaviors, quantityZeroInner)
	setIfNil(&m.CustomErrorResponses, quantityZeroInner)
	setIfNil(&m.Logging, loggingOffInner)
	setIfNil(&m.Restrictions, restrictionsNoneInner)
	setIfNil(&m.ViewerCertificate, viewerCertDefaultInner)
	setIfNil(&m.PriceClass, priceClassAll)
	setIfNil(&m.HTTPVersion, httpVersionDefault)
	setIfNil(&m.IsIPV6Enabled, boolTrueValue)

	if m.Origins != nil {
		m.Origins.applyDefaults()
	}

	if m.DefaultCacheBehavior != nil {
		m.DefaultCacheBehavior.applyDefaults()
	}
}

// applyDefaults fills the per-origin scalars CloudFront always returns.
func (o *originsModel) applyDefaults() {
	for i := range o.Items {
		setIfNil(&o.Items[i].ConnectionAttempts, connAttemptsDefault)
		setIfNil(&o.Items[i].ConnectionTimeout, connTimeoutDefault)
		setIfNil(&o.Items[i].CustomHeaders, quantityZeroInner)
	}
}

// applyDefaults fills the default-cache-behavior blocks the reader dereferences
// unconditionally, then the caching sub-blocks.
func (b *behaviorModel) applyDefaults() {
	setIfNil(&b.TrustedSigners, trustedOffInner)
	setIfNil(&b.TrustedKeyGroups, trustedOffInner)
	setIfNil(&b.LambdaFunctionAssociations, quantityZeroInner)
	setIfNil(&b.FunctionAssociations, quantityZeroInner)
	b.defaultAllowedMethods()
	b.defaultCaching()
}

// defaultAllowedMethods ensures AllowedMethods and its nested CachedMethods are
// present (real CloudFront always returns both; the reader dereferences
// AllowedMethods.CachedMethods without a nil check).
func (b *behaviorModel) defaultAllowedMethods() {
	if b.AllowedMethods == nil {
		b.AllowedMethods = &allowedMethodsModel{
			Quantity: &xmlRaw{Inner: []byte(allowedQtyTwo)},
			Items:    &xmlRaw{Inner: []byte(getHeadItemsInner)},
		}
	}

	setIfNil(&b.AllowedMethods.CachedMethods, cachedMethodsAllInner)
}

// defaultCaching fills the legacy ForwardedValues sub-blocks and TTLs. A behavior
// keyed on a cache policy is mutually exclusive with ForwardedValues/TTLs, so
// those are left untouched when a CachePolicyId is present.
func (b *behaviorModel) defaultCaching() {
	if b.hasCachePolicy() {
		return
	}

	if b.ForwardedValues == nil {
		b.ForwardedValues = &forwardedValuesModel{}
	}

	b.ForwardedValues.applyDefaults()
	setIfNil(&b.MinTTL, minTTLZero)
	setIfNil(&b.DefaultTTL, defaultTTLValue)
	setIfNil(&b.MaxTTL, maxTTLValue)
}

// hasCachePolicy reports whether the behavior references a (non-empty) cache
// policy, in which case legacy ForwardedValues defaults must not be injected.
func (b *behaviorModel) hasCachePolicy() bool {
	return b.CachePolicyID != nil && len(bytes.TrimSpace(b.CachePolicyID.Inner)) > 0
}

// applyDefaults fills the Headers/QueryStringCacheKeys/Cookies sub-blocks a
// legacy ForwardedValues always carries.
func (f *forwardedValuesModel) applyDefaults() {
	setIfNil(&f.QueryString, boolFalseValue)
	setIfNil(&f.Headers, quantityZeroInner)
	setIfNil(&f.QueryStringCacheKeys, quantityZeroInner)

	if f.Cookies == nil {
		f.Cookies = &cookiesModel{Forward: &xmlRaw{Inner: []byte(noneValue)}}
	}

	setIfNil(&f.Cookies.WhitelistedNames, quantityZeroInner)
}
