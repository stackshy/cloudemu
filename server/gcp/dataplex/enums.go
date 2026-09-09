package dataplex

import (
	"encoding/json"
	"errors"
)

// Zone type and resource-spec location-type enums, and the asset resource-spec
// type enum, that a create/patch body must use a valid member of. Real Dataplex
// rejects an out-of-range value with a 400, which CloudEmu mirrors so a
// mis-typed Terraform config fails the same way it would against the real API.
//
//nolint:gochecknoglobals // immutable allowed-value sets
var (
	zoneTypes         = map[string]bool{"RAW": true, "CURATED": true}
	zoneLocationTypes = map[string]bool{"SINGLE_REGION": true, "MULTI_REGION": true}
	assetResourceType = map[string]bool{"STORAGE_BUCKET": true, "BIGQUERY_DATASET": true}
)

var (
	errZoneTypeRequired         = errors.New("zone type is required and must be one of RAW, CURATED")
	errZoneResourceSpecRequired = errors.New("zone resourceSpec.locationType is required and must be one of SINGLE_REGION, MULTI_REGION")
	errAssetResourceSpecType    = errors.New("asset resourceSpec.type is required and must be one of STORAGE_BUCKET, BIGQUERY_DATASET")
)

// validateZone enforces the zone type and resourceSpec.locationType enums. Both
// are required on a zone the real API accepts; an absent or out-of-range value is
// a 400. A patch that omits the field (not in the mask) is allowed to pass so an
// unrelated update is not blocked.
func validateZone(fields map[string]json.RawMessage) error {
	if raw, ok := fields["type"]; ok {
		if !validEnum(raw, zoneTypes) {
			return errZoneTypeRequired
		}
	}

	spec, ok := objectField(fields, "resourceSpec")
	if !ok {
		return nil
	}

	if raw, has := spec["locationType"]; has && !validEnum(raw, zoneLocationTypes) {
		return errZoneResourceSpecRequired
	}

	return nil
}

// validateAsset enforces the asset resourceSpec.type enum. A patch that omits the
// resourceSpec is allowed to pass.
func validateAsset(fields map[string]json.RawMessage) error {
	spec, ok := objectField(fields, "resourceSpec")
	if !ok {
		return nil
	}

	raw, has := spec["type"]
	if !has {
		return nil
	}

	if !validEnum(raw, assetResourceType) {
		return errAssetResourceSpecType
	}

	return nil
}

// validEnum reports whether raw is a JSON string that is a member of allowed.
func validEnum(raw json.RawMessage, allowed map[string]bool) bool {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return false
	}

	return allowed[s]
}
