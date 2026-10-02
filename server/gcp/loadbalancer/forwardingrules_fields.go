package loadbalancer

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	lbdriver "github.com/stackshy/cloudemu/v2/services/loadbalancer/driver"
)

// Reserved tag keys for the forwarding-rule fields added on top of the base set
// in operations.go, plus the generation counters behind the fingerprints.
const (
	frAllPortsTag          = "cloudemu:gcpFrAllPorts"
	frPortsTag             = "cloudemu:gcpFrPorts"
	frLabelsTag            = "cloudemu:gcpFrLabels"
	frIPVersionTag         = "cloudemu:gcpFrIPVersion"
	frNetworkTierTag       = "cloudemu:gcpFrNetworkTier"
	frAllowGlobalAccessTag = "cloudemu:gcpFrAllowGlobalAccess"
	frGenerationTag        = "cloudemu:gcpFrGeneration"
	bsGenerationTag        = "cloudemu:gcpBsGeneration"
)

const (
	actionSetLabels = "setLabels"
	// emptyLabelFingerprint is the labelFingerprint real GCP returns for a
	// resource with no labels.
	emptyLabelFingerprint = "42WmSpB8rSM="
	// maxForwardingRulePorts is the API limit on ports[].
	maxForwardingRulePorts = 5
	defaultNetworkTier     = "PREMIUM"
)

// setLabelsRequest is the forwardingRules.setLabels body.
type setLabelsRequest struct {
	Labels           map[string]string `json:"labels"`
	LabelFingerprint string            `json:"labelFingerprint,omitempty"`
}

// forwardingRulePatchRequest is the subset of a forwardingRules.patch body the
// emulator applies. The other fields of a forwarding rule are immutable.
type forwardingRulePatchRequest struct {
	Description       string `json:"description,omitempty"`
	NetworkTier       string `json:"networkTier,omitempty"`
	AllowGlobalAccess *bool  `json:"allowGlobalAccess,omitempty"`
	Fingerprint       string `json:"fingerprint,omitempty"`
}

// validateForwardingRulePorts rejects the port combinations real GCP rejects.
func validateForwardingRulePorts(req *forwardingRuleRequest) error {
	if req.AllPorts != nil && *req.AllPorts && (req.PortRange != "" || len(req.Ports) > 0) {
		return cerrors.New(cerrors.InvalidArgument,
			"Invalid value for field 'resource.allPorts': 'true'. portRange and ports cannot be used with allPorts.")
	}

	if len(req.Ports) > maxForwardingRulePorts {
		return cerrors.New(cerrors.InvalidArgument,
			"Invalid value for field 'resource.ports': too many ports, at most 5 are allowed.")
	}

	return nil
}

// mergeForwardingRuleExtraTags stores the optional insert fields that have no
// driver equivalent. Pointer fields are stored only when sent, so an unset
// allPorts or allowGlobalAccess reads back as absent rather than false.
func mergeForwardingRuleExtraTags(tags map[string]string, req *forwardingRuleRequest) {
	if req.AllPorts != nil {
		tags[frAllPortsTag] = strconv.FormatBool(*req.AllPorts)
	}

	if req.AllowGlobalAccess != nil {
		tags[frAllowGlobalAccessTag] = strconv.FormatBool(*req.AllowGlobalAccess)
	}

	if len(req.Ports) > 0 {
		encodeJSONTag(tags, frPortsTag, req.Ports)
	}

	if len(req.Labels) > 0 {
		encodeJSONTag(tags, frLabelsTag, req.Labels)
	}

	if req.IPVersion != "" {
		tags[frIPVersionTag] = req.IPVersion
	}

	if req.NetworkTier != "" {
		tags[frNetworkTierTag] = req.NetworkTier
	}
}

// applyForwardingRuleExtras fills the response fields backed by the extra tags.
//
//nolint:gocritic // rp is a request-scoped value
func applyForwardingRuleExtras(out *forwardingRuleResponse, lb *lbdriver.LBInfo, rp gcprest.ResourcePath, host string) {
	if rp.Scope == gcprest.ScopeRegions {
		out.Region = regionLink(host, rp.Project, rp.ScopeName)
	}

	out.AllPorts = boolTag(lb.Tags, frAllPortsTag)
	out.AllowGlobalAccess = boolTag(lb.Tags, frAllowGlobalAccessTag)
	decodeJSONTag(lb.Tags, frPortsTag, &out.Ports)
	decodeJSONTag(lb.Tags, frLabelsTag, &out.Labels)
	out.IPVersion = lb.Tags[frIPVersionTag]
	out.NetworkTier = tagOrDefault(lb.Tags, frNetworkTierTag, defaultNetworkTier)
	out.LabelFingerprint = labelFingerprint(out.Labels)
	out.Fingerprint = generationFingerprint(out.Name, lb.Tags, frGenerationTag)
}

// usesPortList reports whether the rule forwards allPorts or an explicit ports
// list, in which case it has no portRange.
func usesPortList(tags map[string]string) bool {
	if b := boolTag(tags, frAllPortsTag); b != nil && *b {
		return true
	}

	return tags[frPortsTag] != ""
}

// regionLink is the self-link of a region.
func regionLink(host, project, region string) string {
	return host + gcprest.BasePrefix + "projects/" + project + "/regions/" + region
}

// labelFingerprint is a content hash of labels, so equal labels always give the
// same fingerprint across reads, restarts and snapshot restores.
func labelFingerprint(labels map[string]string) string {
	if len(labels) == 0 {
		return emptyLabelFingerprint
	}

	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	var sb strings.Builder

	sb.WriteString("labels:")

	for _, k := range keys {
		sb.WriteString(k + "=" + labels[k] + "\x00")
	}

	var b [8]byte

	binary.BigEndian.PutUint64(b[:], fnvHash(sb.String()))

	return base64.StdEncoding.EncodeToString(b[:])
}

// generationOf reads a generation counter tag, treating absent or malformed as 0.
func generationOf(tags map[string]string, key string) int {
	n, err := strconv.Atoi(tags[key])
	if err != nil {
		return 0
	}

	return n
}

// bumpGeneration advances the generation counter so the next fingerprint differs.
func bumpGeneration(tags map[string]string, key string) {
	tags[key] = strconv.Itoa(generationOf(tags, key) + 1)
}

// generationFingerprint changes on every mutation. Generation 0 keeps the
// name-only value so records written before the counter existed read the same.
func generationFingerprint(name string, tags map[string]string, key string) string {
	gen := generationOf(tags, key)
	if gen == 0 {
		return fingerprintOf(name)
	}

	return fingerprintOf(name + ":" + strconv.Itoa(gen))
}

// writeConditionNotMet answers a stale fingerprint with GCP's 412.
func writeConditionNotMet(w http.ResponseWriter, msg string) {
	gcprest.WriteError(w, http.StatusPreconditionFailed, "conditionNotMet", msg)
}

// errStaleFingerprint marks a mutate closure that rejected a stale fingerprint.
var errStaleFingerprint = cerrors.New(cerrors.FailedPrecondition, "stale fingerprint")

// setForwardingRuleLabels serves forwardingRules.setLabels (global and regional).
// Labels are replaced, not merged, and a non-empty labelFingerprint must match
// the current labels.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) setForwardingRuleLabels(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	var req setLabelsRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	h.mutateForwardingRule(w, r, rp, actionSetLabels,
		"Labels fingerprint either invalid or resource labels have changed",
		func(lb *lbdriver.LBInfo) error {
			var cur map[string]string

			decodeJSONTag(lb.Tags, frLabelsTag, &cur)

			if req.LabelFingerprint != "" && req.LabelFingerprint != labelFingerprint(cur) {
				return errStaleFingerprint
			}

			delete(lb.Tags, frLabelsTag)

			if len(req.Labels) > 0 {
				encodeJSONTag(lb.Tags, frLabelsTag, req.Labels)
			}

			return nil
		})
}

// patchForwardingRule serves forwardingRules.patch for the mutable fields
// (allowGlobalAccess, networkTier, description). An omitted fingerprint is
// accepted, since Terraform's allow_global_access update sends none.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) patchForwardingRule(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	var req forwardingRulePatchRequest
	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	h.mutateForwardingRule(w, r, rp, "patch",
		"Fingerprint either invalid or resource has changed",
		func(lb *lbdriver.LBInfo) error {
			name := displayName(lb.Tags, frNameTag, lb.Name)
			if req.Fingerprint != "" && req.Fingerprint != generationFingerprint(name, lb.Tags, frGenerationTag) {
				return errStaleFingerprint
			}

			if req.AllowGlobalAccess != nil {
				lb.Tags[frAllowGlobalAccessTag] = strconv.FormatBool(*req.AllowGlobalAccess)
			}

			if req.NetworkTier != "" {
				lb.Tags[frNetworkTierTag] = req.NetworkTier
			}

			if req.Description != "" {
				lb.Tags[frDescriptionTag] = req.Description
			}

			return nil
		})
}

// mutateForwardingRule applies mutate to the rule through the provider, bumps
// its generation and answers with a DONE operation of type opType.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) mutateForwardingRule(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath,
	opType, staleMsg string, mutate func(*lbdriver.LBInfo) error,
) {
	patcher, ok := h.lb.(lbdriver.GCPForwardingRulePatcher)
	if !ok {
		gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented", "load balancer driver cannot patch forwarding rules")
		return
	}

	err := patcher.PatchGCPForwardingRule(r.Context(), scopedDriverName(rp, rp.ResourceName), func(lb *lbdriver.LBInfo) error {
		if err := mutate(lb); err != nil {
			return err
		}

		bumpGeneration(lb.Tags, frGenerationTag)

		return nil
	})

	switch {
	case errors.Is(err, errStaleFingerprint):
		writeConditionNotMet(w, staleMsg)
		return
	case err != nil:
		gcprest.WriteCErr(w, err)
		return
	}

	op := h.ops.RecordDone(hostOf(r), rp.Project, rp.Scope, rp.ScopeName,
		resourceForwardingRules, rp.ResourceName, opType)

	gcprest.WriteJSON(w, http.StatusOK, op)
}
