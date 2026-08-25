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

// Reserved tag keys the dns driver's ZoneInfo does not model, so they
// round-trip through the zone's tags: the DNS suffix (dnsName), the
// human-readable description, and the RFC3339 creation time.
const (
	dnsNameTag      = "cloudemu:gcpDnsName"
	descriptionTag  = "cloudemu:gcpDescription"
	creationTimeTag = "cloudemu:gcpCreationTime"
)

// reservedTagCount is the number of reserved tags above, used to size the tag
// map created at zone creation.
const reservedTagCount = 3

// Kind values Cloud DNS stamps on its resources; the SDK tolerates them being
// absent but real responses carry them, so we mirror the wire faithfully.
const (
	kindManagedZone            = "dns#managedZone"
	kindManagedZonesList       = "dns#managedZonesListResponse"
	kindResourceRecordSet      = "dns#resourceRecordSet"
	kindResourceRecordSetsList = "dns#resourceRecordSetsListResponse"
	kindChange                 = "dns#change"
	kindChangesList            = "dns#changesListResponse"
	kindOperation              = "dns#operation"
)

// changeStatusDone is the terminal state Cloud DNS reports once a change has
// propagated; our mock applies changes synchronously so every change is done.
const changeStatusDone = "done"

// operationStatusDone is the terminal state a managed-zone Operation reports;
// zone mutations apply synchronously so every operation is immediately done.
const operationStatusDone = "done"

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
	NameServers  []string          `json:"nameServers,omitempty"`
}

type managedZonesListResponse struct {
	Kind          string            `json:"kind"`
	ManagedZones  []managedZoneJSON `json:"managedZones"`
	NextPageToken string            `json:"nextPageToken,omitempty"`
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
	Kind          string                  `json:"kind"`
	Rrsets        []resourceRecordSetJSON `json:"rrsets"`
	NextPageToken string                  `json:"nextPageToken,omitempty"`
}

// changesListResponse is the Cloud DNS changes.list response.
type changesListResponse struct {
	Kind          string       `json:"kind"`
	Changes       []changeJSON `json:"changes"`
	NextPageToken string       `json:"nextPageToken,omitempty"`
}

// operationJSON is the Cloud DNS Operation resource, returned by
// managedZones.patch and managedZones.update. Zone mutations apply
// synchronously, so the operation is reported done immediately.
type operationJSON struct {
	Kind      string `json:"kind"`
	ID        string `json:"id,omitempty"`
	StartTime string `json:"startTime,omitempty"`
	Status    string `json:"status,omitempty"`
	Type      string `json:"type,omitempty"`
	User      string `json:"user,omitempty"`
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
		Kind:         kindManagedZone,
		Name:         info.Name,
		ID:           numericID(info.ID),
		Visibility:   visibilityFor(info.Private),
		Labels:       stripReservedTags(info.Tags),
		DNSName:      dnsName,
		Description:  info.Tags[descriptionTag],
		CreationTime: info.Tags[creationTimeTag],
		NameServers:  nameServersFor(info.ID),
	}
}

// nsLetterCount is the number of ns-cloud-<letter> pools Cloud DNS draws from.
const nsLetterCount = 5

// nameServersPerZone is the number of authoritative name servers Cloud DNS
// assigns to every managed zone.
const nameServersPerZone = 4

// nameServersFor returns the four authoritative name servers Cloud DNS assigns
// to a zone, e.g. ns-cloud-e1.googledomains.com. … ns-cloud-e4.googledomains.com.
// Real Cloud DNS picks one of five letter pools (a–e) per zone; we derive it
// deterministically from the zone id so a given zone always reports the same
// delegation NS set on every create/get and in its apex NS record.
func nameServersFor(zoneID string) []string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(zoneID))
	letter := rune('a' + int(h.Sum32()%nsLetterCount))

	ns := make([]string, 0, nameServersPerZone)
	for i := 1; i <= nameServersPerZone; i++ {
		ns = append(ns, "ns-cloud-"+string(letter)+strconv.Itoa(i)+".googledomains.com.")
	}

	return ns
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

// apexTTL is the TTL Cloud DNS gives the auto-created apex SOA and NS records.
const apexTTL = 21600

// apexRecordConfigs returns the SOA and NS record sets Cloud DNS auto-creates at
// a new zone's apex (dnsName). The NS record advertises the zone's delegation
// name servers; the SOA names the first of them as primary.
func apexRecordConfigs(zoneID, dnsName string) []dnsdriver.RecordConfig {
	ns := nameServersFor(zoneID)
	soa := ns[0] + " cloud-dns-hostmaster.google.com. 1 21600 3600 259200 300"

	return []dnsdriver.RecordConfig{
		{ZoneID: zoneID, Name: dnsName, Type: "NS", TTL: apexTTL, Values: ns},
		{ZoneID: zoneID, Name: dnsName, Type: "SOA", TTL: apexTTL, Values: []string{soa}},
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
