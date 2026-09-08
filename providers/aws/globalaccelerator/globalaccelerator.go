// Package globalaccelerator provides an in-memory mock implementation of the AWS
// Global Accelerator control plane: accelerators, their listeners and the
// endpoint groups under each listener, plus per-accelerator flow-log attributes
// and resource tags.
//
// The mock is control-plane only — it routes no real traffic and runs no health
// checks. An accelerator is created synchronously with stable computed fields
// (arn, two deterministic static IPv4 addresses, dnsName, dualStackDnsName,
// status, createdTime) minted once at create and stored, so repeated reads never
// drift; Status settles to DEPLOYED at once so an IaC waiter completes. Global
// Accelerator is a GLOBAL service: its ARNs carry an empty region field.
package globalaccelerator

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/globalaccelerator/driver"
)

// Compile-time check that Mock implements driver.GlobalAccelerator.
var _ driver.GlobalAccelerator = (*Mock)(nil)

// serviceName is the ARN service marker; Global Accelerator ARNs carry an empty
// region field, so the control-plane region is not part of the ARN.
const serviceName = "globalaccelerator"

// defaultMaxResults caps a page when the caller requests none.
const defaultMaxResults = 100

// statusDeployed is the terminal accelerator status. The real service passes
// through IN_PROGRESS for minutes; the emulator settles to DEPLOYED synchronously
// so an IaC waiter never hangs.
const statusDeployed = "DEPLOYED"

// IP address type and family values.
const (
	ipAddressTypeIPv4      = "IPV4"
	ipAddressTypeDualStack = "DUAL_STACK"
	ipFamilyIPv4           = "IPv4"
	ipFamilyIPv6           = "IPv6"
)

// Endpoint-group and listener defaults, applied when the caller omits a value so
// reads are stable and match the real service.
const (
	defaultClientAffinity      = "NONE"
	defaultHealthCheckProtocol = "TCP"
	defaultHealthCheckInterval = int32(30)
	defaultThresholdCount      = int32(3)
	defaultTrafficDial         = float64(100)
	defaultEndpointWeight      = int32(128)
	endpointHealthInitial      = "INITIAL"
)

// dnsHexLen is the number of hex characters in an accelerator's DNS label.
const dnsHexLen = 16

// ARN segment counts distinguishing the three resource kinds; the resource part
// is "accelerator/<a>[/listener/<l>[/endpoint-group/<e>]]".
const (
	segsAccelerator   = 2
	segsListener      = 4
	segsEndpointGroup = 6
)

// Mock is an in-memory implementation of the AWS Global Accelerator control
// plane. Accelerators, listeners and endpoint groups are each keyed by their own
// ARN; attributes are keyed by accelerator ARN.
type Mock struct {
	accelerators   *memstore.Store[driver.Accelerator]
	listeners      *memstore.Store[driver.Listener]
	endpointGroups *memstore.Store[driver.EndpointGroup]
	attributes     *memstore.Store[driver.AcceleratorAttributes]
	opts           *config.Options
}

// New creates a new Global Accelerator mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		accelerators:   memstore.New[driver.Accelerator](),
		listeners:      memstore.New[driver.Listener](),
		endpointGroups: memstore.New[driver.EndpointGroup](),
		attributes:     memstore.New[driver.AcceleratorAttributes](),
		opts:           opts,
	}
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// acceleratorARN mints the stable ARN reported for an accelerator. Global
// Accelerator is a global service, so the region field is empty.
func (m *Mock) acceleratorARN(id string) string {
	return idgen.AWSARN(serviceName, "", m.opts.AccountID, "accelerator/"+id)
}

// listenerARN nests a listener ARN under its accelerator ARN.
func listenerARN(acceleratorArn, id string) string {
	return acceleratorArn + "/listener/" + id
}

// endpointGroupARN nests an endpoint-group ARN under its listener ARN.
func endpointGroupARN(listenerArn, id string) string {
	return listenerArn + "/endpoint-group/" + id
}

// arnResource returns the resource portion of an ARN (everything after the sixth
// colon-separated field), or "" when the ARN is malformed. Global Accelerator's
// empty region still counts as a field.
func arnResource(arn string) string {
	const arnFields = 6

	parts := strings.SplitN(arn, ":", arnFields)
	if len(parts) < arnFields {
		return ""
	}

	return parts[arnFields-1]
}

// classifyARN reports the resource kind named by an ARN from its segment count.
func classifyARN(arn string) (segs int) {
	res := arnResource(arn)
	if res == "" {
		return 0
	}

	return len(strings.Split(res, "/"))
}

// dnsLabel derives the stable 16-hex DNS label for an accelerator id.
func dnsLabel(id string) string {
	sum := sha256.Sum256([]byte("dns:" + id))

	return "a" + hex.EncodeToString(sum[:])[:dnsHexLen]
}

// staticIPv4Addresses derives two deterministic, stable static IPv4 addresses for
// an accelerator id. The addresses are minted once at create and stored, so they
// never change across reads.
func staticIPv4Addresses(id string) []string {
	sum := sha256.Sum256([]byte("ip:" + id))

	return []string{ipv4From(sum[0:4]), ipv4From(sum[4:8])}
}

// ipv4From renders four bytes as a routable-looking IPv4 dotted quad, avoiding 0
// and 255 in the first and last octets.
func ipv4From(b []byte) string {
	const (
		octetMod = 254
		octetOff = 1
	)

	a := int(b[0])%octetMod + octetOff
	d := int(b[3])%octetMod + octetOff

	return strconv.Itoa(a) + "." + strconv.Itoa(int(b[1])) + "." + strconv.Itoa(int(b[2])) + "." + strconv.Itoa(d)
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}

	return append([]string(nil), in...)
}

func copyPortRanges(in []driver.PortRange) []driver.PortRange {
	if in == nil {
		return nil
	}

	return append([]driver.PortRange(nil), in...)
}

func copyPortOverrides(in []driver.PortOverride) []driver.PortOverride {
	if in == nil {
		return nil
	}

	return append([]driver.PortOverride(nil), in...)
}

func copyIPSets(in []driver.IPSet) []driver.IPSet {
	if in == nil {
		return nil
	}

	out := make([]driver.IPSet, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].IPAddresses = copyStrings(in[i].IPAddresses)
	}

	return out
}

func copyInt32Ptr(in *int32) *int32 {
	if in == nil {
		return nil
	}

	v := *in

	return &v
}

func copyBoolPtr(in *bool) *bool {
	if in == nil {
		return nil
	}

	v := *in

	return &v
}

func copyFloat64Ptr(in *float64) *float64 {
	if in == nil {
		return nil
	}

	v := *in

	return &v
}

func copyEndpointDescriptions(in []driver.EndpointDescription) []driver.EndpointDescription {
	if in == nil {
		return nil
	}

	out := make([]driver.EndpointDescription, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].Weight = copyInt32Ptr(in[i].Weight)
		out[i].ClientIPPreservationEnabled = copyBoolPtr(in[i].ClientIPPreservationEnabled)
	}

	return out
}

// copyAccelerator returns an alias-free copy of an accelerator.
func copyAccelerator(a *driver.Accelerator) driver.Accelerator {
	out := *a
	out.IPSets = copyIPSets(a.IPSets)
	out.Tags = copyTags(a.Tags)

	return out
}

// copyListener returns an alias-free copy of a listener.
func copyListener(l *driver.Listener) driver.Listener {
	out := *l
	out.PortRanges = copyPortRanges(l.PortRanges)
	out.Tags = copyTags(l.Tags)

	return out
}

// copyEndpointGroup returns an alias-free copy of an endpoint group.
func copyEndpointGroup(g *driver.EndpointGroup) driver.EndpointGroup {
	out := *g
	out.EndpointDescriptions = copyEndpointDescriptions(g.EndpointDescriptions)
	out.TrafficDialPercentage = copyFloat64Ptr(g.TrafficDialPercentage)
	out.HealthCheckPort = copyInt32Ptr(g.HealthCheckPort)
	out.HealthCheckIntervalSeconds = copyInt32Ptr(g.HealthCheckIntervalSeconds)
	out.ThresholdCount = copyInt32Ptr(g.ThresholdCount)
	out.PortOverrides = copyPortOverrides(g.PortOverrides)
	out.Tags = copyTags(g.Tags)

	return out
}

// filterValues returns the elements of in for which keep reports true, indexing
// rather than copying each element so a large struct is not duplicated per
// iteration.
func filterValues[T any](in []T, keep func(*T) bool) []T {
	out := make([]T, 0, len(in))

	for i := range in {
		if keep(&in[i]) {
			out = append(out, in[i])
		}
	}

	return out
}

// anyValue reports whether any element of in satisfies match, indexing rather
// than copying each element.
func anyValue[T any](in []T, match func(*T) bool) bool {
	for i := range in {
		if match(&in[i]) {
			return true
		}
	}

	return false
}

// paginateCopies returns a deep-copied, paginated page of matched and its next
// token, sharing the offset/window mechanics across the list operations.
func paginateCopies[T any](matched []T, page driver.Page, cp func(*T) T) (items []*T, next string) {
	start, end, tok := paginate(len(matched), page)
	items = make([]*T, 0, end-start)

	for i := start; i < end; i++ {
		v := cp(&matched[i])
		items = append(items, &v)
	}

	return items, tok
}

// paginate returns the offset window and next token for a slice of length n,
// honoring an opaque numeric offset token.
func paginate(n int, page driver.Page) (start, end int, next string) {
	start = decodeToken(page.NextToken)
	if start > n {
		start = n
	}

	limit := int(page.MaxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	end = start + limit
	if end >= n {
		return start, n, ""
	}

	return start, end, encodeToken(end)
}

func encodeToken(offset int) string {
	return strconv.Itoa(offset)
}

func decodeToken(token string) int {
	if token == "" {
		return 0
	}

	n, err := strconv.Atoi(token)
	if err != nil || n < 0 {
		return 0
	}

	return n
}
