package eventarc

import (
	"context"
	"fmt"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	crdriver "github.com/stackshy/cloudemu/v2/services/cloudrun/driver"
	sdrv "github.com/stackshy/cloudemu/v2/services/serverless/driver"
)

// Known Eventarc direct/Pub/Sub event types this mock validates a trigger's
// "type" eventFilter against. Real Eventarc's full catalog is served by
// projects.locations.providers.list and is far larger (Firebase, BigQuery,
// Cloud Composer, …); this is the commonly used subset — Pub/Sub, Cloud Audit
// Logs, Cloud Storage, and Firestore — CloudEmu validates against.
const (
	eventTypePubsubMessagePublished = "google.cloud.pubsub.topic.v1.messagePublished"
	eventTypeAuditLogWritten        = "google.cloud.audit.log.v1.written"
	eventTypeStorageFinalized       = "google.cloud.storage.object.v1.finalized"
	eventTypeStorageDeleted         = "google.cloud.storage.object.v1.deleted"
	eventTypeStorageArchived        = "google.cloud.storage.object.v1.archived"
	eventTypeStorageMetadataUpdated = "google.cloud.storage.object.v1.metadataUpdated"
	eventTypeFirestoreCreated       = "google.cloud.firestore.document.v1.created"
	eventTypeFirestoreUpdated       = "google.cloud.firestore.document.v1.updated"
	eventTypeFirestoreDeleted       = "google.cloud.firestore.document.v1.deleted"
	eventTypeFirestoreWritten       = "google.cloud.firestore.document.v1.written"
)

// knownEventTypes is the static lookup table validateEventFilters checks a
// trigger's "type" filter value against.
var knownEventTypes = map[string]struct{}{ //nolint:gochecknoglobals // static lookup table
	eventTypePubsubMessagePublished: {},
	eventTypeAuditLogWritten:        {},
	eventTypeStorageFinalized:       {},
	eventTypeStorageDeleted:         {},
	eventTypeStorageArchived:        {},
	eventTypeStorageMetadataUpdated: {},
	eventTypeFirestoreCreated:       {},
	eventTypeFirestoreUpdated:       {},
	eventTypeFirestoreDeleted:       {},
	eventTypeFirestoreWritten:       {},
}

// eventFilter attribute names validateEventFilters looks for.
const (
	filterAttrType        = "type"
	filterAttrServiceName = "serviceName"
	filterAttrMethodName  = "methodName"
)

// validateEventFilters checks a trigger's eventFilters against Eventarc's own
// admission-time rules: a required "type" filter naming a known event type,
// and — for the audit-log event type — required "serviceName" and
// "methodName" filters pinning the trigger to one API method (real Eventarc
// refuses to create an unscoped audit-log trigger). Returns "" when the
// filters are valid, or the INVALID_ARGUMENT message otherwise.
func validateEventFilters(filters []eventFilterJSON) string {
	typeValue, hasType := filterValue(filters, filterAttrType)
	if !hasType {
		return "eventFilters must include a non-empty 'type' filter"
	}

	if _, known := knownEventTypes[typeValue]; !known {
		return fmt.Sprintf("eventFilters 'type' %q is not a known Eventarc event type", typeValue)
	}

	if typeValue != eventTypeAuditLogWritten {
		return ""
	}

	if _, ok := filterValue(filters, filterAttrServiceName); !ok {
		return "an audit-log trigger requires a non-empty 'serviceName' filter"
	}

	if _, ok := filterValue(filters, filterAttrMethodName); !ok {
		return "an audit-log trigger requires a non-empty 'methodName' filter"
	}

	return ""
}

// filterValue returns the value of the first filter with the given attribute
// and whether one was present with a non-empty value.
func filterValue(filters []eventFilterJSON, attr string) (string, bool) {
	for i := range filters {
		if filters[i].Attribute == attr && filters[i].Value != "" {
			return filters[i].Value, true
		}
	}

	return "", false
}

// FunctionResolver checks whether a Cloud Function destination exists, so
// Create/Patch can reject a trigger before it is stored as a dead route.
// sdrv.Serverless satisfies this via GetFunction.
type FunctionResolver interface {
	GetFunction(ctx context.Context, name string) (*sdrv.FunctionInfo, error)
}

// CloudRunResolver is the Cloud Run analog of FunctionResolver.
// crdriver.CloudRun satisfies this via GetService.
type CloudRunResolver interface {
	GetService(ctx context.Context, name string) (*crdriver.Service, error)
}

// validateDestination checks that dest names an existing Cloud Run service or
// Cloud Function, mirroring Eventarc's own admission-time validation that a
// trigger can't route to a resource the caller can't demonstrate exists.
// Workflow destinations aren't modeled (no workflows driver in CloudEmu), so
// they pass unchecked, and a resolver left unwired (nil — the standalone
// package server may not have the peer service) skips its half of the check
// gracefully rather than failing closed.
func (h *Handler) validateDestination(ctx context.Context, dest *destinationJSON) error {
	switch {
	case dest.CloudFunction != "":
		if h.functions == nil {
			return nil
		}

		name := lastSegment(dest.CloudFunction)

		if _, err := h.functions.GetFunction(ctx, name); err != nil {
			return cerrors.Newf(cerrors.NotFound, "destination cloud function %q not found", dest.CloudFunction)
		}
	case dest.CloudRun != nil && dest.CloudRun.Service != "":
		if h.cloudRun == nil {
			return nil
		}

		if _, err := h.cloudRun.GetService(ctx, dest.CloudRun.Service); err != nil {
			return cerrors.Newf(cerrors.NotFound, "destination cloud run service %q not found", dest.CloudRun.Service)
		}
	}

	return nil
}

// lastSegment returns the trailing path segment of a resource name (or the
// input itself when it has no "/"). Mirrors providers/gcp/eventarc's helper
// of the same name — the two packages can't share it (provider must not
// import server).
func lastSegment(name string) string {
	trimmed := strings.TrimRight(name, "/")
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		return trimmed[i+1:]
	}

	return trimmed
}
