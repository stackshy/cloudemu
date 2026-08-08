package idgen

import (
	"fmt"
	"strings"
)

// DefaultRealm is the OCI realm used when none is configured.
const DefaultRealm = "oc1"

// regionCodeLen is the length of the region code embedded in an OCID.
const regionCodeLen = 3

// regionCodes maps an OCI region name to the three-letter code that appears
// inside an OCID.
//
//nolint:gochecknoglobals // static lookup table, never mutated
var regionCodes = map[string]string{
	"af-johannesburg-1": "jnb",
	"ap-chuncheon-1":    "yny",
	"ap-hyderabad-1":    "hyd",
	"ap-melbourne-1":    "mel",
	"ap-mumbai-1":       "bom",
	"ap-osaka-1":        "kix",
	"ap-seoul-1":        "icn",
	"ap-singapore-1":    "sin",
	"ap-sydney-1":       "syd",
	"ap-tokyo-1":        "nrt",
	"ca-montreal-1":     "yul",
	"ca-toronto-1":      "yyz",
	"eu-amsterdam-1":    "ams",
	"eu-frankfurt-1":    "fra",
	"eu-madrid-1":       "mad",
	"eu-marseille-1":    "mrs",
	"eu-milan-1":        "lin",
	"eu-paris-1":        "cdg",
	"eu-stockholm-1":    "arn",
	"eu-zurich-1":       "zrh",
	"il-jerusalem-1":    "mtz",
	"me-abudhabi-1":     "auh",
	"me-dubai-1":        "dxb",
	"me-jeddah-1":       "jed",
	"mx-monterrey-1":    "mty",
	"mx-queretaro-1":    "qro",
	"sa-bogota-1":       "bog",
	"sa-santiago-1":     "scl",
	"sa-saopaulo-1":     "gru",
	"sa-valparaiso-1":   "vap",
	"sa-vinhedo-1":      "vcp",
	"uk-cardiff-1":      "cwl",
	"uk-london-1":       "lhr",
	"us-ashburn-1":      "iad",
	"us-chicago-1":      "ord",
	"us-phoenix-1":      "phx",
	"us-sanjose-1":      "sjc",
}

// RegionCode returns the three-letter code for an OCI region name. An
// unrecognized region falls back to its city segment, so regions this table
// predates still get distinct codes.
func RegionCode(region string) string {
	if code, ok := regionCodes[region]; ok {
		return code
	}

	if region == "" {
		return ""
	}

	parts := strings.Split(strings.ToLower(region), "-")

	city := parts[0]
	if len(parts) > 1 {
		city = parts[1]
	}

	if len(city) >= regionCodeLen {
		return city[:regionCodeLen]
	}

	return city
}

// OCID generates an Oracle Cloud Identifier of the form
// ocid1.<type>.<realm>.<region>.<unique>. Identity resources are global and
// pass an empty region, producing the doubled dot real OCI emits.
func OCID(resourceType, realm, region string) string {
	if realm == "" {
		realm = DefaultRealm
	}

	return fmt.Sprintf("ocid1.%s.%s.%s.%s", resourceType, realm, RegionCode(region), uniqueSuffix())
}

// GlobalOCID generates an OCID for a region-agnostic identity resource.
func GlobalOCID(resourceType, realm string) string {
	return OCID(resourceType, realm, "")
}

// uniqueSuffix returns the opaque trailing segment of an OCID, keeping the
// leading "a" run of a real one and the shared monotonic counter.
func uniqueSuffix() string {
	return fmt.Sprintf("aaaaaaaa%016x", next())
}
