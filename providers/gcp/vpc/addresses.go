package vpc

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/fnv"
	"net"
	"sort"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Compile-time check that Mock implements the reserved-address capability.
var _ driver.GCPAddressStore = (*Mock)(nil)

// reservedIPBase is the start of the synthetic range handed out to reserved
// addresses the caller did not pin to a specific IP.
const reservedIPBase = "10.128.0.0"

// fieldLabels / fieldLabelFingerprint are the address body members the
// provider owns.
const (
	fieldLabels           = "labels"
	fieldLabelFingerprint = "labelFingerprint"
)

func addressKey(project, scope, name string) string {
	return project + "/" + scope + "/" + name
}

// cloneAddress deep-copies a stored address so a caller can never alias the
// stored body.
func cloneAddress(a *driver.GCPAddress) driver.GCPAddress {
	out := *a
	out.Body = append(json.RawMessage(nil), a.Body...)

	return out
}

// InsertGCPAddress stores a new reserved address and stamps its
// labelFingerprint from the labels it was created with.
func (m *Mock) InsertGCPAddress(_ context.Context, addr driver.GCPAddress) error {
	obj, err := addressObject(addr.Body)
	if err != nil {
		return err
	}

	obj[fieldLabelFingerprint] = addressLabelFingerprint(labelsOf(obj))

	body, err := json.Marshal(obj)
	if err != nil {
		return cerrors.Newf(cerrors.Internal, "encode address %q: %v", addr.Name, err)
	}

	stored := addr
	stored.Body = body

	if !m.addresses.SetIfAbsent(addressKey(addr.Project, addr.Scope, addr.Name), &stored) {
		return cerrors.Newf(cerrors.AlreadyExists, "The resource 'addresses/%s' already exists", addr.Name)
	}

	return nil
}

// GetGCPAddress returns the named address, or NotFound.
func (m *Mock) GetGCPAddress(_ context.Context, project, scope, name string) (*driver.GCPAddress, error) {
	a, ok := m.addresses.Get(addressKey(project, scope, name))
	if !ok {
		return nil, addressNotFound(name)
	}

	out := cloneAddress(a)

	return &out, nil
}

// ListGCPAddresses returns a project's addresses in scope (every scope when
// scope is empty), ordered by scope then name.
func (m *Mock) ListGCPAddresses(_ context.Context, project, scope string) ([]driver.GCPAddress, error) {
	matched := m.addresses.Filter(func(_ string, a *driver.GCPAddress) bool {
		return a.Project == project && (scope == "" || a.Scope == scope)
	})

	out := make([]driver.GCPAddress, 0, len(matched))
	for _, a := range matched {
		out = append(out, cloneAddress(a))
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}

		return out[i].Name < out[j].Name
	})

	return out, nil
}

// DeleteGCPAddress removes the named address, or returns NotFound.
func (m *Mock) DeleteGCPAddress(_ context.Context, project, scope, name string) error {
	if !m.addresses.Delete(addressKey(project, scope, name)) {
		return addressNotFound(name)
	}

	return nil
}

// AllocateGCPAddressIP hands out the next IP of the synthetic reserved range.
// The counter is part of the snapshot, so a restored emulator never hands out
// an IP a restored address already holds.
func (m *Mock) AllocateGCPAddressIP(_ context.Context) (string, error) {
	n := m.addressIPSeq.Add(1)

	out := make(net.IP, net.IPv4len)
	binary.BigEndian.PutUint32(out, binary.BigEndian.Uint32(net.ParseIP(reservedIPBase).To4())+n)

	return out.String(), nil
}

// SetGCPAddressLabels replaces an address's labels under the store lock, so the
// fingerprint check and the write are atomic against a concurrent setLabels.
func (m *Mock) SetGCPAddressLabels(_ context.Context, project, scope, name string,
	labels map[string]string, fingerprint string,
) error {
	var opErr error

	found := m.addresses.Update(addressKey(project, scope, name), func(a *driver.GCPAddress) *driver.GCPAddress {
		obj, err := addressObject(a.Body)
		if err != nil {
			opErr = err
			return a
		}

		if fingerprint == "" || fingerprint != addressLabelFingerprint(labelsOf(obj)) {
			opErr = cerrors.New(cerrors.FailedPrecondition,
				"Labels fingerprint either invalid or resource labels have changed")

			return a
		}

		if len(labels) == 0 {
			delete(obj, fieldLabels)
		} else {
			obj[fieldLabels] = labels
		}

		obj[fieldLabelFingerprint] = addressLabelFingerprint(labels)

		body, err := json.Marshal(obj)
		if err != nil {
			opErr = cerrors.Newf(cerrors.Internal, "encode address %q: %v", name, err)
			return a
		}

		next := *a
		next.Body = body

		return &next
	})
	if !found {
		return addressNotFound(name)
	}

	return opErr
}

// addressObject decodes a stored or submitted address body into an object.
func addressObject(body json.RawMessage) (map[string]any, error) {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
		return nil, cerrors.New(cerrors.InvalidArgument, "address body is not a JSON object")
	}

	return obj, nil
}

// labelsOf reads the string labels of a decoded address body.
func labelsOf(obj map[string]any) map[string]string {
	raw, _ := obj[fieldLabels].(map[string]any)
	out := make(map[string]string, len(raw))

	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}

	return out
}

// addressLabelFingerprint is a pure function of the label set, so it changes
// exactly when the labels do, and an address with no labels still has a
// stable, non-empty fingerprint the caller must echo back, as real Compute
// Engine requires.
func addressLabelFingerprint(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	h := fnv.New64a()
	_, _ = h.Write([]byte("labels\x00"))

	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(labels[k]))
		_, _ = h.Write([]byte{0})
	}

	var b [8]byte

	binary.BigEndian.PutUint64(b[:], h.Sum64())

	return base64.StdEncoding.EncodeToString(b[:])
}

// addressNotFound renders compute's not-found message for an address.
func addressNotFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "The resource 'addresses/%s' was not found", name)
}
