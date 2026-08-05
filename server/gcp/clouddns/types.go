package clouddns

import (
	"context"
	"hash/fnv"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// dnsNameTag stores a zone's DNS suffix (dnsName), which the dns driver does
// not model, so it round-trips through the zone's tags.
const dnsNameTag = "cloudemu:gcpDnsName"

// Kind values Cloud DNS stamps on its resources; the SDK tolerates them being
// absent but real responses carry them, so we mirror the wire faithfully.
const (
	kindManagedZone            = "dns#managedZone"
	kindManagedZonesList       = "dns#managedZonesListResponse"
	kindResourceRecordSet      = "dns#resourceRecordSet"
	kindResourceRecordSetsList = "dns#resourceRecordSetsListResponse"
	kindChange                 = "dns#change"
)

// changeStatusDone is the terminal state Cloud DNS reports once a change has
// propagated; our mock applies changes synchronously so every change is done.
const changeStatusDone = "done"

// managedZoneJSON is the Cloud DNS ManagedZone resource. The SDK unmarshals
// `id` as a uint64 (via a `,string` tag), so it must serialize as a numeric
// string — see numericID for how the driver's zone-<uuid> id is folded down.
type managedZoneJSON struct {
	Kind         string            `json:"kind"`
	Name         string            `json:"name"`
	DNSName      string            `json:"dnsName,omitempty"`
	Description  string            `json:"description,omitempty"`
	ID           string            `json:"id,omitempty"`
	Visibility   string            `json:"visibility,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	CreationTime string            `json:"creationTime,omitempty"`
}

type managedZonesListResponse struct {
	Kind         string            `json:"kind"`
	ManagedZones []managedZoneJSON `json:"managedZones"`
}

// resourceRecordSetJSON is the Cloud DNS ResourceRecordSet resource.
type resourceRecordSetJSON struct {
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	TTL     int64    `json:"ttl"`
	Rrdatas []string `json:"rrdatas,omitempty"`
}

type resourceRecordSetsListResponse struct {
	Kind   string                  `json:"kind"`
	Rrsets []resourceRecordSetJSON `json:"rrsets"`
}

// changeJSON is the Cloud DNS Change resource: a batch of record additions and
// deletions applied atomically.
type changeJSON struct {
	Kind      string                  `json:"kind"`
	ID        string                  `json:"id,omitempty"`
	Additions []resourceRecordSetJSON `json:"additions,omitempty"`
	Deletions []resourceRecordSetJSON `json:"deletions,omitempty"`
	Status    string                  `json:"status,omitempty"`
	StartTime string                  `json:"startTime,omitempty"`
}

// visibilityFor maps the driver's private flag to Cloud DNS visibility.
func visibilityFor(private bool) string {
	if private {
		return "private"
	}

	return "public"
}

// privateFor is the inverse of visibilityFor.
func privateFor(visibility string) bool {
	return visibility == "private"
}

// numericID folds the driver's zone-<uuid> id into a stable numeric string,
// which is what the SDK's uint64 `id` field requires on the wire.
func numericID(id string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))

	return strconv.FormatUint(h.Sum64(), 10)
}

func toManagedZoneJSON(info *dnsdriver.ZoneInfo) managedZoneJSON {
	// dnsName is the DNS suffix (e.g. "example.com."), which the driver doesn't
	// model, so it's stashed in a reserved tag at create. Fall back to the zone
	// name only when absent.
	dnsName := info.Name
	if v, ok := info.Tags[dnsNameTag]; ok && v != "" {
		dnsName = v
	}

	return managedZoneJSON{
		Kind:       kindManagedZone,
		Name:       info.Name,
		ID:         numericID(info.ID),
		Visibility: visibilityFor(info.Private),
		Labels:     stripReservedTags(info.Tags),
		DNSName:    dnsName,
	}
}

// stripReservedTags returns the user labels with cloudemu-internal keys removed.
func stripReservedTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))

	for k, v := range in {
		if strings.HasPrefix(k, "cloudemu:") {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func toRecordSetJSON(rec *dnsdriver.RecordInfo) resourceRecordSetJSON {
	return resourceRecordSetJSON{
		Kind:    kindResourceRecordSet,
		Name:    rec.Name,
		Type:    rec.Type,
		TTL:     int64(rec.TTL),
		Rrdatas: rec.Values,
	}
}

// resolveZoneID maps the SDK-facing {managedZone} URL segment to the driver's
// internal zone id by scanning the zone list. Cloud DNS accepts either the zone
// name or its numeric id there, so both are matched (the numeric id is the
// FNV-folded value this handler hands back as ManagedZone.Id). Returns NotFound
// if no zone matches.
func (h *Handler) resolveZoneID(ctx context.Context, project, nameOrID string) (string, error) {
	zones, err := h.dns.ListZones(ctx, scope.Scope{Project: project})
	if err != nil {
		return "", err
	}

	for i := range zones {
		if zones[i].Name == nameOrID || numericID(zones[i].ID) == nameOrID {
			return zones[i].ID, nil
		}
	}

	return "", cerrors.Newf(cerrors.NotFound, "managed zone %q not found", nameOrID)
}
