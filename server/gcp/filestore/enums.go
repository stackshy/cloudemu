package filestore

import (
	"encoding/json"
	"strings"
)

// Filestore enums arrive on the wire as either the canonical STRING name (REST /
// Terraform / gcloud) or the protobuf INTEGER value (GAPIC gRPC-transcoded
// clients). The maps below mirror the google.cloud.filestore.v1 protobuf enum
// values exactly, so an int normalizes to the same canonical name a string
// request carries, and both round-trip to the STRING form real Filestore emits.

// nullLiteral is the JSON null token, treated as an absent value on decode.
const nullLiteral = "null"

// tierNames maps Instance.Tier integers to canonical names.
//
//nolint:gochecknoglobals // immutable protobuf enum lookup table
var tierNames = map[int32]string{
	0: "TIER_UNSPECIFIED",
	1: "STANDARD",
	2: "PREMIUM",
	3: "BASIC_HDD",
	4: "BASIC_SSD",
	5: "HIGH_SCALE_SSD",
	6: "ENTERPRISE",
	7: "ZONAL",
	8: "REGIONAL",
}

// accessModeNames maps NfsExportOptions.AccessMode integers to canonical names.
//
//nolint:gochecknoglobals // immutable protobuf enum lookup table
var accessModeNames = map[int32]string{
	0: "ACCESS_MODE_UNSPECIFIED",
	1: "READ_ONLY",
	2: "READ_WRITE",
}

// squashModeNames maps NfsExportOptions.SquashMode integers to canonical names.
//
//nolint:gochecknoglobals // immutable protobuf enum lookup table
var squashModeNames = map[int32]string{
	0: "SQUASH_MODE_UNSPECIFIED",
	1: "NO_ROOT_SQUASH",
	2: "ROOT_SQUASH",
}

// addressModeNames maps NetworkConfig.AddressMode (modes[]) integers to names.
//
//nolint:gochecknoglobals // immutable protobuf enum lookup table
var addressModeNames = map[int32]string{
	0: "ADDRESS_MODE_UNSPECIFIED",
	1: "MODE_IPV4",
	2: "MODE_IPV6",
}

// connectModeNames maps NetworkConfig.ConnectMode integers to canonical names.
//
//nolint:gochecknoglobals // immutable protobuf enum lookup table
var connectModeNames = map[int32]string{
	0: "CONNECT_MODE_UNSPECIFIED",
	1: "DIRECT_PEERING",
	2: "PRIVATE_SERVICE_ACCESS",
	3: "PRIVATE_SERVICE_CONNECT",
}

// rawEnum captures an enum field that arrives as a JSON string (canonical name)
// or a JSON number (protobuf value), deferring name resolution to normalize.
type rawEnum struct {
	str   string
	num   int32
	isNum bool
	set   bool
}

// UnmarshalJSON accepts a quoted enum name or a bare integer.
func (e *rawEnum) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == nullLiteral {
		return nil
	}

	e.set = true

	if s[0] == '"' {
		return json.Unmarshal(b, &e.str)
	}

	e.isNum = true

	return json.Unmarshal(b, &e.num)
}

// normalize resolves the raw token to a canonical enum name using the field's
// value table. present reports whether the field was supplied; ok reports
// whether the supplied token names a known enum value.
func (e rawEnum) normalize(names map[int32]string) (canonical string, ok, present bool) {
	if !e.set {
		return "", false, false
	}

	if e.isNum {
		name, exists := names[e.num]
		return name, exists, true
	}

	up := strings.ToUpper(strings.TrimSpace(e.str))
	for _, name := range names {
		if name == up {
			return up, true, true
		}
	}

	return "", false, true
}
