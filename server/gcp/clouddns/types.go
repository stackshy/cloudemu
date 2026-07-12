package clouddns

import (
	"context"
	"hash/fnv"
	"strconv"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
)

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
	return managedZoneJSON{
		Kind:       kindManagedZone,
		Name:       info.Name,
		ID:         numericID(info.ID),
		Visibility: visibilityFor(info.Private),
		Labels:     info.Tags,
		DNSName:    info.Name,
	}
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
func (h *Handler) resolveZoneID(ctx context.Context, nameOrID string) (string, error) {
	zones, err := h.dns.ListZones(ctx)
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
