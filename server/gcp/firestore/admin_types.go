package firestore

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// Database enum canonical names and their protobuf integer values. The Admin
// GAPIC REST client sends enums as integers ($alt=json;enum-encoding=int),
// while the discovery/Terraform client sends the string names, so requests are
// normalized through these tables and responses always emit the string name
// (which every client's protojson decoder accepts).
const (
	dbTypeNative    = "FIRESTORE_NATIVE"
	dbTypeDatastore = "DATASTORE_MODE"

	concurrencyOptimistic   = "OPTIMISTIC"
	concurrencyPessimistic  = "PESSIMISTIC"
	concurrencyOptimisticEG = "OPTIMISTIC_WITH_ENTITY_GROUPS"

	appEngineEnabled  = "ENABLED"
	appEngineDisabled = "DISABLED"

	pitrEnabled  = "POINT_IN_TIME_RECOVERY_ENABLED"
	pitrDisabled = "POINT_IN_TIME_RECOVERY_DISABLED"

	deleteProtectionEnabled  = "DELETE_PROTECTION_ENABLED"
	deleteProtectionDisabled = "DELETE_PROTECTION_DISABLED"
)

//nolint:gochecknoglobals // static enum lookup tables (int→name) shared by request normalization.
var (
	dbTypeByInt = map[int32]string{1: dbTypeNative, 2: dbTypeDatastore}

	concurrencyByInt = map[int32]string{
		1: concurrencyOptimistic, 2: concurrencyPessimistic, 3: concurrencyOptimisticEG,
	}

	appEngineByInt = map[int32]string{1: appEngineEnabled, 2: appEngineDisabled}

	pitrByInt = map[int32]string{1: pitrEnabled, 2: pitrDisabled}

	deleteProtectionByInt = map[int32]string{1: deleteProtectionDisabled, 2: deleteProtectionEnabled}
)

// buildDBRecord constructs a new database record from a create body, filling
// defaults for omitted fields and validating enum values.
func buildDBRecord(project, databaseID string, body map[string]any) (dbRecord, error) {
	now := time.Now().UTC()

	rec := dbRecord{
		project:             project,
		databaseID:          databaseID,
		locationID:          stringField(body, "locationId"),
		dbType:              dbTypeNative,
		concurrencyMode:     concurrencyOptimistic,
		appEngineMode:       appEngineDisabled,
		pointInTimeRecovery: pitrDisabled,
		deleteProtection:    deleteProtectionDisabled,
		uid:                 uuid4(),
		createTime:          now,
		updateTime:          now,
	}

	if err := assignEnums(&rec, body); err != nil {
		return dbRecord{}, err
	}

	rec.etag = newEtag()

	return rec, nil
}

// patchDBRecord applies body (guided by mask, or all present fields when mask is
// nil) to a copy of cur and returns it. Only mutable configuration fields are
// honored; identity/timestamp fields are ignored.
func patchDBRecord(cur *dbRecord, body map[string]any, mask map[string]bool) (dbRecord, error) {
	next := *cur

	if maskWants(mask, "type", body) {
		v, err := enumValue(body["type"], dbTypeByInt, "type")
		if err != nil {
			return dbRecord{}, err
		}

		next.dbType = v
	}

	if err := patchModes(&next, body, mask); err != nil {
		return dbRecord{}, err
	}

	next.updateTime = time.Now().UTC()
	next.etag = newEtag()

	return next, nil
}

// patchModes applies the concurrency / delete-protection / PITR / app-engine
// enum fields of a patch, keeping patchDBRecord within complexity limits.
func patchModes(next *dbRecord, body map[string]any, mask map[string]bool) error {
	fields := []struct {
		key   string
		table map[int32]string
		dst   *string
	}{
		{"concurrencyMode", concurrencyByInt, &next.concurrencyMode},
		{"appEngineIntegrationMode", appEngineByInt, &next.appEngineMode},
		{"pointInTimeRecoveryEnablement", pitrByInt, &next.pointInTimeRecovery},
		{"deleteProtectionState", deleteProtectionByInt, &next.deleteProtection},
	}

	for _, f := range fields {
		if !maskWants(mask, f.key, body) {
			continue
		}

		v, err := enumValue(body[f.key], f.table, f.key)
		if err != nil {
			return err
		}

		*f.dst = v
	}

	return nil
}

// assignEnums normalizes the enum-typed create fields present in body.
func assignEnums(rec *dbRecord, body map[string]any) error {
	assignments := []struct {
		key   string
		table map[int32]string
		dst   *string
	}{
		{"type", dbTypeByInt, &rec.dbType},
		{"concurrencyMode", concurrencyByInt, &rec.concurrencyMode},
		{"appEngineIntegrationMode", appEngineByInt, &rec.appEngineMode},
		{"pointInTimeRecoveryEnablement", pitrByInt, &rec.pointInTimeRecovery},
		{"deleteProtectionState", deleteProtectionByInt, &rec.deleteProtection},
	}

	for _, a := range assignments {
		raw, present := body[a.key]
		if !present {
			continue
		}

		v, err := enumValue(raw, a.table, a.key)
		if err != nil {
			return err
		}

		*a.dst = v
	}

	return nil
}

// renderDatabase builds the JSON map for a Database resource, emitting enums as
// their canonical string names.
func renderDatabase(rec *dbRecord) map[string]any {
	return map[string]any{
		"name":                          dbKey(rec.project, rec.databaseID),
		"uid":                           rec.uid,
		"type":                          rec.dbType,
		"concurrencyMode":               rec.concurrencyMode,
		"appEngineIntegrationMode":      rec.appEngineMode,
		"pointInTimeRecoveryEnablement": rec.pointInTimeRecovery,
		"deleteProtectionState":         rec.deleteProtection,
		"locationId":                    rec.locationID,
		"versionRetentionPeriod":        versionRetentionDefault,
		"earliestVersionTime":           rec.updateTime.Format(time.RFC3339Nano),
		"createTime":                    rec.createTime.Format(time.RFC3339Nano),
		"updateTime":                    rec.updateTime.Format(time.RFC3339Nano),
		"etag":                          rec.etag,
	}
}

// databaseAnyResponse wraps a rendered database as a google.longrunning
// Operation response Any (adds the @type the GAPIC LRO poller needs).
func databaseAnyResponse(rec *dbRecord) map[string]any {
	out := renderDatabase(rec)
	out["@type"] = "type.googleapis.com/google.firestore.admin.v1.Database"

	return out
}

// maskWants reports whether a patch should apply key: true when the field is in
// the mask, or (absent a mask) when it is present in the body.
func maskWants(mask map[string]bool, key string, body map[string]any) bool {
	if mask != nil {
		return mask[key]
	}

	_, present := body[key]

	return present
}

// enumValue normalizes a raw JSON enum value (string name or protobuf int) to
// its canonical string name, validating it against table.
func enumValue(raw any, table map[int32]string, field string) (string, error) {
	switch v := raw.(type) {
	case string:
		if v == "" {
			return "", cerrors.Newf(cerrors.InvalidArgument, "empty value for %q", field)
		}

		for _, name := range table {
			if name == v {
				return v, nil
			}
		}

		return "", cerrors.Newf(cerrors.InvalidArgument, "invalid value %q for %q", v, field)
	case float64:
		if name, ok := table[int32(v)]; ok {
			return name, nil
		}

		return "", cerrors.Newf(cerrors.InvalidArgument, "invalid enum %v for %q", v, field)
	default:
		return "", cerrors.Newf(cerrors.InvalidArgument, "invalid type for %q", field)
	}
}

// stringField returns body[key] as a string, or "" when absent or not a string.
func stringField(body map[string]any, key string) string {
	if s, ok := body[key].(string); ok {
		return s
	}

	return ""
}

// uuid4 returns a random RFC-4122 v4 UUID string for a resource uid.
func uuid4() string {
	var b [16]byte

	if _, err := rand.Read(b[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}

	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// newEtag returns a fresh opaque etag.
func newEtag() string {
	var b [8]byte

	if _, err := rand.Read(b[:]); err != nil {
		return "etag"
	}

	return hex.EncodeToString(b[:])
}
