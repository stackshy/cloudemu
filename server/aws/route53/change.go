package route53

import (
	"crypto/rand"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/server/wire"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// changeIDLen is the number of characters after the "C" prefix in a change id.
const changeIDLen = 14

// changeIDAlphabet is the uppercase-alphanumeric set Route 53 change ids use.
const changeIDAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// newChangeID returns a distinct "/change/C..." id so two changes are
// distinguishable.
func newChangeID() string {
	buf := make([]byte, changeIDLen)
	if _, err := rand.Read(buf); err != nil {
		return "/change/C" + itoa(fnv1a(time.Now().String()))
	}

	for i := range buf {
		buf[i] = changeIDAlphabet[int(buf[i])%len(changeIDAlphabet)]
	}

	return "/change/C" + string(buf)
}

// nowRFC3339 is the submitted-at timestamp helper for change responses.
func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// getChange answers GetChange. Every change the mock returns is applied
// synchronously, so any change id is reported INSYNC — this unblocks the SDK's
// ResourceRecordSetsChanged waiter and propagation polling.
func (h *Handler) getChange(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	id = strings.TrimPrefix(id, "/")
	id = strings.TrimPrefix(id, "change/")

	wire.WriteXML(w, http.StatusOK, getChangeResponse{
		Xmlns: xmlns,
		ChangeInfo: changeInfoXML{
			Id:          "/change/" + id,
			Status:      changeStatusInsync,
			SubmittedAt: nowRFC3339(),
		},
	})
}

// getHostedZoneCount answers GetHostedZoneCount with the number of zones in the
// account.
func (h *Handler) getHostedZoneCount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	infos, err := h.dns.ListZones(r.Context(), scope.Scope{})
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteXML(w, http.StatusOK, getHostedZoneCountResponse{
		Xmlns:           xmlns,
		HostedZoneCount: int64(len(infos)),
	})
}

// listHostedZonesByName answers ListHostedZonesByName, returning zones sorted by
// name with an optional dnsname start position.
func (h *Handler) listHostedZonesByName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	infos, err := h.dns.ListZones(r.Context(), scope.Scope{})
	if err != nil {
		writeErr(w, err)
		return
	}

	sort.Slice(infos, func(i, j int) bool {
		return strings.ToLower(infos[i].Name) < strings.ToLower(infos[j].Name)
	})

	dnsName := r.URL.Query().Get("dnsname")
	if start := strings.ToLower(dnsName); start != "" {
		for len(infos) > 0 && strings.ToLower(infos[0].Name) < start {
			infos = infos[1:]
		}
	}

	zones := make([]hostedZoneXML, 0, len(infos))
	for i := range infos {
		zones = append(zones, toHostedZoneXML(&infos[i]))
	}

	wire.WriteXML(w, http.StatusOK, listHostedZonesByNameResponse{
		Xmlns:       xmlns,
		HostedZones: zones,
		DNSName:     dnsName,
		IsTruncated: false,
		MaxItems:    listMaxItems,
	})
}

// testDNSAnswer answers TestDNSAnswer by resolving the requested record against
// the named zone, reporting NOERROR with the record's values or NXDOMAIN.
func (h *Handler) testDNSAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w)
		return
	}

	q := r.URL.Query()
	zoneID := trimZonePrefix(q.Get("hostedzoneid"))
	// Records are stored as FQDNs, so normalize the queried name to a trailing
	// dot to match whether the client sent it dotted or not.
	name := ensureTrailingDot(q.Get("recordname"))
	rtype := q.Get("recordtype")

	resp := testDNSAnswerResponse{
		Xmlns:        xmlns,
		Nameserver:   nameServersFor(zoneID)[0],
		RecordName:   name,
		RecordType:   rtype,
		ResponseCode: "NXDOMAIN",
		Protocol:     "UDP",
	}

	if rec, err := h.dns.GetRecord(r.Context(), zoneID, name, rtype); err == nil {
		resp.ResponseCode = "NOERROR"
		resp.RecordData = append([]string(nil), rec.Values...)
	}

	wire.WriteXML(w, http.StatusOK, resp)
}

// nameServerTLDs are the suffixes AWS spreads a delegation set's four name
// servers across.
//
//nolint:gochecknoglobals // static lookup table.
var nameServerTLDs = []string{"com", "net", "org", "co.uk"}

// nameServersFor returns the four deterministic authoritative name servers for a
// hosted zone. Deriving them from the zone id keeps Get/Create consistent so a
// registrar reads back the same delegation set every time.
func nameServersFor(zoneID string) []string {
	h := fnv1a(zoneID)

	out := make([]string, len(nameServerTLDs))
	for i, tld := range nameServerTLDs {
		n := (h + uint64(i)*7919) % 4000
		out[i] = "ns-" + itoa(n) + ".awsdns-" + itoa(n%64) + "." + tld
	}

	return out
}

// fnv1a is a tiny FNV-1a hash used to derive stable name-server numbers.
func fnv1a(s string) uint64 {
	const (
		offset = 1469598103934665603
		prime  = 1099511628211
	)

	h := uint64(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}

	return h
}

// itoa renders a small unsigned number without pulling strconv into hot paths.
func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}

	var buf [20]byte
	i := len(buf)

	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}

	return string(buf[i:])
}
