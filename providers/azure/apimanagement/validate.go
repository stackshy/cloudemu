package apimanagement

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

const (
	// maxServiceNameLen is the longest service name Azure accepts.
	maxServiceNameLen = 50

	// skuConsumption is the serverless tier, the only one whose capacity is 0.
	skuConsumption = "Consumption"
	// skuPremium is the only classic tier that can be deployed across
	// availability zones.
	skuPremium = "Premium"
)

// Sentinels the HTTP layer maps to APIM's specific ARM error codes. Each is
// joined onto a canonical cerrors value, so cerrors.GetCode/Message still work.
var (
	// ErrNameNotAvailable: the service name (a global *.azure-api.net DNS
	// label) is already used by another live service.
	ErrNameNotAvailable = errors.New("api management service name not available")
	// ErrSoftDeleted: the name belongs to a soft-deleted service that must be
	// recovered (properties.restore) or purged first.
	ErrSoftDeleted = errors.New("api management service is soft-deleted")
	// ErrLocationMismatch: a PUT on an existing service names a different
	// location, which is immutable.
	ErrLocationMismatch = errors.New("api management service location is immutable")
)

// serviceNamePattern is Azure's service-name rule: starts with a letter, then
// letters, digits and hyphens, and does not end with a hyphen.
var serviceNamePattern = regexp.MustCompile(`^[A-Za-z]([A-Za-z0-9-]*[A-Za-z0-9])?$`)

// skuInfo is a tier's canonical casing and its maximum unit count per region
// (0 = no emulator-enforced ceiling).
type skuInfo struct {
	name        string
	maxCapacity int32
}

// validSKUs is the armapimanagement v3 SKUType enum, keyed lowercase so the
// lookup is case-insensitive like ARM, mapped to the canonical casing and the
// per-region unit ceiling Azure publishes for the tier: Developer 1, Basic 2,
// Standard 4, Premium 12, BasicV2/StandardV2 10 (the azurerm provider's own
// sku_name validation uses the same ceilings, except that it lets Premium go to
// 99 for support-raised quotas). Isolated is by-request only and has no
// ceiling here.
//
//nolint:gochecknoglobals // static lookup table of published limits
var validSKUs = map[string]skuInfo{
	"developer":   {"Developer", 1},
	"basic":       {"Basic", 2},
	"standard":    {"Standard", 4},
	"premium":     {skuPremium, 12},
	"consumption": {skuConsumption, 0},
	"isolated":    {"Isolated", 0},
	"basicv2":     {"BasicV2", 10},
	"standardv2":  {"StandardV2", 10},
}

// canonicalSKU returns the canonical casing of a known SKU name, or the input
// unchanged when it is unknown (validation rejects unknown names first).
func canonicalSKU(name string) string {
	if c, ok := validSKUs[strings.ToLower(name)]; ok {
		return c.name
	}

	return name
}

// validateCreate rejects a create/replace request with missing or malformed
// required fields: the path identity, location, the SKU block, the zones and
// the two publisher properties.
func validateCreate(sub, rg, name, location string, in *ServiceInput) error {
	switch {
	case sub == "":
		return invalid("subscription is required")
	case rg == "":
		return invalid("resource group is required")
	case location == "":
		return invalid("location is required")
	}

	if err := validateName(name); err != nil {
		return err
	}

	if in.SkuName == nil || *in.SkuName == "" {
		return invalid("sku.name is required")
	}

	if in.SkuCapacity == nil {
		return invalid("sku.capacity is required")
	}

	if err := validateSKU(*in.SkuName, *in.SkuCapacity); err != nil {
		return err
	}

	if err := validateZones(*in.SkuName, in.Zones); err != nil {
		return err
	}

	return validatePublisher(in.Properties)
}

// validateName enforces Azure's service-name rule (1-50 characters, starts with
// a letter, letters/digits/hyphens, no trailing hyphen).
func validateName(name string) error {
	if !validName(name) {
		return cerrors.Newf(cerrors.InvalidArgument,
			"invalid API Management service name %q: it must be 1-%d characters, start with a letter, "+
				"contain only letters, digits and hyphens, and not end with a hyphen", name, maxServiceNameLen)
	}

	return nil
}

// validName reports whether name satisfies Azure's service-name rule.
func validName(name string) bool {
	return name != "" && len(name) <= maxServiceNameLen && serviceNamePattern.MatchString(name)
}

// validateSKU checks the SKU name is a known tier and the capacity fits it: the
// Consumption tier must be 0 units, every other tier at least 1 and at most the
// tier's published ceiling.
func validateSKU(name string, capacity int32) error {
	info, ok := validSKUs[strings.ToLower(name)]
	if !ok {
		return cerrors.Newf(cerrors.InvalidArgument, "invalid sku.name %q", name)
	}

	if info.name == skuConsumption {
		if capacity != 0 {
			return cerrors.Newf(cerrors.InvalidArgument,
				"sku.capacity must be 0 for the Consumption tier, got %d", capacity)
		}

		return nil
	}

	if capacity < 1 {
		return cerrors.Newf(cerrors.InvalidArgument,
			"sku.capacity must be at least 1 for the %s tier, got %d", info.name, capacity)
	}

	if info.maxCapacity > 0 && capacity > info.maxCapacity {
		return cerrors.Newf(cerrors.InvalidArgument,
			"sku.capacity must be at most %d for the %s tier, got %d", info.maxCapacity, info.name, capacity)
	}

	return nil
}

// validateZones allows availability zones only on the Premium tier.
func validateZones(skuName string, zones []string) error {
	if len(zones) == 0 || canonicalSKU(skuName) == skuPremium {
		return nil
	}

	return cerrors.Newf(cerrors.InvalidArgument,
		"availability zones are supported only in the Premium tier, not %s", canonicalSKU(skuName))
}

// validatePublisher requires non-empty properties.publisherEmail and
// properties.publisherName.
func validatePublisher(props json.RawMessage) error {
	var p struct {
		PublisherEmail string `json:"publisherEmail"`
		PublisherName  string `json:"publisherName"`
	}

	if len(props) > 0 {
		if err := json.Unmarshal(props, &p); err != nil {
			return cerrors.Newf(cerrors.InvalidArgument, "malformed properties: %v", err)
		}
	}

	switch {
	case strings.TrimSpace(p.PublisherEmail) == "":
		return invalid("properties.publisherEmail is required")
	case strings.TrimSpace(p.PublisherName) == "":
		return invalid("properties.publisherName is required")
	default:
		return nil
	}
}

// restoreRequested reports whether the request body sets properties.restore,
// the flag that recovers a soft-deleted service instead of creating one.
func restoreRequested(props json.RawMessage) bool {
	var p struct {
		Restore bool `json:"restore"`
	}

	if len(props) == 0 || json.Unmarshal(props, &p) != nil {
		return false
	}

	return p.Restore
}

// invalid is an InvalidArgument error (APIM 400 ValidationError).
func invalid(msg string) error {
	return cerrors.New(cerrors.InvalidArgument, msg)
}

// coded joins a sentinel onto a canonical error so the HTTP layer can pick the
// APIM-specific ARM code while generic callers still see the cerrors code.
func coded(sentinel error, err *cerrors.Error) error {
	return errors.Join(err, sentinel)
}
