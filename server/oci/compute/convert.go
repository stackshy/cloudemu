package compute

import (
	"strings"

	ocicompute "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	"github.com/stackshy/cloudemu/v2/services/compute"
)

// internalTagPrefix marks the tags CloudEmu keeps on a resource to carry OCI
// attributes the portable projections have no field for. They are stripped
// from freeformTags on the wire.
const internalTagPrefix = "cloudemu:"

// tagDisplayName is the display name of a resource whose portable projection
// has no name field.
const tagDisplayName = ocicompute.TagDisplayName

// OCI instance lifecycle states.
const (
	lifecycleProvisioning = "PROVISIONING"
	lifecycleRunning      = "RUNNING"
	lifecycleStarting     = "STARTING"
	lifecycleStopping     = "STOPPING"
	lifecycleStopped      = "STOPPED"
	lifecycleTerminating  = "TERMINATING"
	lifecycleTerminated   = "TERMINATED"
	lifecycleAvailable    = "AVAILABLE"
	lifecycleDeleted      = "DELETED"
	lifecycleCreating     = "CREATING"
)

// launchModeParavirtualized is the launch mode CloudEmu's images report.
const launchModeParavirtualized = "PARAVIRTUALIZED"

// ocidPrefixParts is the number of dot-separated segments before an OCID's
// resource type.
const ocidPrefixParts = 2

// ocidType returns the resource type segment of an OCID.
func ocidType(id string) string {
	parts := strings.SplitN(id, ".", ocidPrefixParts+1)
	if len(parts) <= ocidPrefixParts {
		return ""
	}

	return parts[1]
}

// instanceLifecycle maps a portable instance state onto OCI's.
func instanceLifecycle(state string) string {
	switch state {
	case compute.StatePending:
		return lifecycleProvisioning
	case compute.StateRunning:
		return lifecycleRunning
	case compute.StateRestarting:
		return lifecycleStarting
	case compute.StateStopping:
		return lifecycleStopping
	case compute.StateStopped:
		return lifecycleStopped
	case compute.StateShuttingDown:
		return lifecycleTerminating
	case compute.StateTerminated:
		return lifecycleTerminated
	default:
		return strings.ToUpper(state)
	}
}

// storageLifecycle maps a portable volume or snapshot state onto OCI's.
func storageLifecycle(state string) string {
	switch state {
	case "available", "in-use", "completed":
		return lifecycleAvailable
	case "pending":
		return lifecycleCreating
	case "deregistered":
		return lifecycleDeleted
	default:
		return strings.ToUpper(state)
	}
}

// freeformOf returns the tags a caller set, without CloudEmu's internal keys.
func freeformOf(tags map[string]string) map[string]string {
	out := make(map[string]string, len(tags))

	for k, v := range tags {
		if !strings.HasPrefix(k, internalTagPrefix) {
			out[k] = v
		}
	}

	return out
}

// withInternal returns the caller's freeform tags plus the internal keys
// carrying OCI attributes, skipping empty values.
func withInternal(freeform map[string]string, kv ...string) map[string]string {
	out := make(map[string]string, len(freeform)+len(kv))

	for k, v := range freeform {
		if !strings.HasPrefix(k, internalTagPrefix) {
			out[k] = v
		}
	}

	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			out[kv[i]] = kv[i+1]
		}
	}

	return out
}

// tagOr reads an internal tag, falling back when it is absent.
func tagOr(tags map[string]string, key, fallback string) string {
	if v, ok := tags[key]; ok {
		return v
	}

	return fallback
}

// toShapeConfig converts OCI's shape config onto the provider's.
func toShapeConfig(w *shapeConfigWire) *ocicompute.ShapeConfig {
	if w == nil {
		return nil
	}

	return &ocicompute.ShapeConfig{
		OCPUs:                     w.Ocpus,
		MemoryInGBs:               w.MemoryInGBs,
		NetworkingBandwidthInGbps: w.NetworkingBandwidthInGbps,
	}
}

// toShapeConfigWire is toShapeConfig's inverse.
func toShapeConfigWire(c *ocicompute.ShapeConfig) *shapeConfigWire {
	if c == nil {
		return nil
	}

	return &shapeConfigWire{
		Ocpus:                     c.OCPUs,
		MemoryInGBs:               c.MemoryInGBs,
		NetworkingBandwidthInGbps: c.NetworkingBandwidthInGbps,
	}
}

// toAgentConfig converts OCI's agent config onto the provider's.
func toAgentConfig(w *agentConfigWire) *ocicompute.AgentConfig {
	if w == nil {
		return nil
	}

	return &ocicompute.AgentConfig{
		IsMonitoringDisabled:  w.IsMonitoringDisabled,
		IsManagementDisabled:  w.IsManagementDisabled,
		AreAllPluginsDisabled: w.AreAllPluginsDisabled,
	}
}

// toAgentConfigWire is toAgentConfig's inverse.
func toAgentConfigWire(c *ocicompute.AgentConfig) *agentConfigWire {
	if c == nil {
		return nil
	}

	return &agentConfigWire{
		IsMonitoringDisabled:  c.IsMonitoringDisabled,
		IsManagementDisabled:  c.IsManagementDisabled,
		AreAllPluginsDisabled: c.AreAllPluginsDisabled,
	}
}

// toSourceDetails converts OCI's source block onto the provider's, defaulting
// the id to whichever typed field carried it.
func toSourceDetails(w *sourceDetailsWire) ocicompute.SourceDetails {
	if w == nil {
		return ocicompute.SourceDetails{}
	}

	id := w.ID
	if id == "" {
		id = firstNonEmpty(w.ImageID, w.BootVolumeID)
	}

	return ocicompute.SourceDetails{
		SourceType:          w.SourceType,
		ID:                  id,
		BootVolumeSizeInGBs: w.BootVolumeSizeInGBs,
	}
}

// toSourceDetailsWire is toSourceDetails' inverse.
func toSourceDetailsWire(s ocicompute.SourceDetails) *sourceDetailsWire {
	if s.SourceType == "" {
		return nil
	}

	out := &sourceDetailsWire{
		SourceType:          s.SourceType,
		ID:                  s.ID,
		BootVolumeSizeInGBs: s.BootVolumeSizeInGBs,
	}

	switch s.SourceType {
	case "image":
		out.ImageID = s.ID
	case "bootVolume":
		out.BootVolumeID = s.ID
	}

	return out
}

// gbsToMBs is the megabyte figure OCI reports alongside every size in GBs.
func gbsToMBs(gbs int) int {
	return gbs * 1024 //nolint:mnd // GB to MB
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}

	return ""
}

// displayNameUpdate is the partial update an OCI update body carries: a
// display name and freeform tags, both optional.
func displayNameUpdate(displayName string, tags map[string]string) ocicompute.Update {
	return ocicompute.Update{DisplayName: optString(displayName), Tags: freeformOf(tags)}
}

// optString is nil for an absent field, so an update leaves it alone.
func optString(v string) *string {
	if v == "" {
		return nil
	}

	return &v
}
