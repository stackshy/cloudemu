package filestore

import (
	"encoding/binary"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

const (
	stateReady = "READY"

	// defaultConnectMode is applied when a network config omits connectMode.
	defaultConnectMode = "DIRECT_PEERING"
	// defaultAccessMode / defaultSquashMode are the nfsExportOptions defaults
	// real Filestore fills in for an option that omits them.
	defaultAccessMode = "READ_WRITE"
	defaultSquashMode = "NO_ROOT_SQUASH"
	// defaultAddressMode is applied when a network config omits modes.
	defaultAddressMode = "MODE_IPV4"

	// ipHostOffset is the host index within a reservedIpRange CIDR assigned to a
	// network's ipAddresses (real Filestore reserves a small block and uses one
	// of its hosts).
	ipHostOffset uint32 = 2

	// privateIPBase is 10.0.0.0, the base for a synthesized ipAddress when no
	// reservedIpRange CIDR is given.
	privateIPBase uint32 = 0x0A000000
)

// instanceModel is the stored, normalized state of one Filestore instance.
type instanceModel struct {
	name        string
	description string
	tier        string
	state       string
	createTime  time.Time
	labels      map[string]string
	fileShares  []fileShareModel
	networks    []networkModel
	etag        string
	kmsKeyName  string
}

type fileShareModel struct {
	name             string
	capacityGb       int64
	sourceBackup     string
	nfsExportOptions []nfsExportModel
}

type nfsExportModel struct {
	ipRanges   []string
	network    string
	accessMode string
	squashMode string
	anonUID    int64
	anonGID    int64
}

type networkModel struct {
	network         string
	modes           []string
	reservedIPRange string
	ipAddresses     []string
	connectMode     string
}

// clone returns a deep copy of the instance so callers can read it (e.g.
// marshal to JSON) after the store lock is released without racing a concurrent
// patch that mutates the stored model's maps and slices in place.
func (m *instanceModel) clone() *instanceModel {
	cp := *m

	if m.labels != nil {
		cp.labels = make(map[string]string, len(m.labels))

		for k, v := range m.labels {
			cp.labels[k] = v
		}
	}

	if m.fileShares != nil {
		cp.fileShares = make([]fileShareModel, len(m.fileShares))

		for i, fs := range m.fileShares {
			fsCopy := fs
			if fs.nfsExportOptions != nil {
				fsCopy.nfsExportOptions = make([]nfsExportModel, len(fs.nfsExportOptions))

				for j, opt := range fs.nfsExportOptions {
					optCopy := opt
					optCopy.ipRanges = append([]string(nil), opt.ipRanges...)
					fsCopy.nfsExportOptions[j] = optCopy
				}
			}

			cp.fileShares[i] = fsCopy
		}
	}

	if m.networks != nil {
		cp.networks = make([]networkModel, len(m.networks))

		for i, n := range m.networks {
			nCopy := n
			nCopy.modes = append([]string(nil), n.modes...)
			nCopy.ipAddresses = append([]string(nil), n.ipAddresses...)
			cp.networks[i] = nCopy
		}
	}

	return &cp
}

// store is the in-memory Filestore control-plane backing state. Filestore has
// no portable driver in cloudemu (the emulator models no NFS data plane), so —
// like Cloud KMS, project IAM and Cloud Billing — the handler owns its state
// here, keyed by full instance resource name.
type store struct {
	mu        sync.RWMutex
	clock     config.Clock
	instances map[string]*instanceModel
	nextOp    int
	nextIP    uint32
}

func newStore(clock config.Clock) *store {
	if clock == nil {
		clock = config.RealClock{}
	}

	return &store{clock: clock, instances: make(map[string]*instanceModel)}
}

// owns reports whether this store holds the named instance. Used by Matches to
// claim only genuinely-Filestore item traffic, letting Memorystore (which shares
// the .../instances path grammar) keep its own.
func (s *store) owns(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.instances[name]

	return ok
}

// ownsAnyIn reports whether this store holds any instance in the given
// (project, location). Used by Matches to claim the bare LIST only when a
// Filestore client is listing, so a pure-Memorystore project's LIST falls
// through.
func (s *store) ownsAnyIn(project, location string) bool {
	prefix := locationPrefix(project, location)

	s.mu.RLock()
	defer s.mu.RUnlock()

	for name := range s.instances {
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			return true
		}
	}

	return false
}

func (s *store) create(name string, m *instanceModel) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.instances[name]; ok {
		return cerrors.Newf(cerrors.AlreadyExists, "instance %s already exists", name)
	}

	m.name = name
	m.state = stateReady
	m.createTime = s.clock.Now()
	s.assignIPs(m)
	s.instances[name] = m

	return nil
}

func (s *store) get(name string) (*instanceModel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	m, ok := s.instances[name]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "instance %s not found", name)
	}

	return m.clone(), nil
}

func (s *store) list(project, location string) []*instanceModel {
	prefix := locationPrefix(project, location)

	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*instanceModel, 0)

	for name, m := range s.instances {
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			out = append(out, m.clone())
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })

	return out
}

// patch applies the masked fields of upd to the stored instance. A nil bit in
// the mask leaves the corresponding field untouched.
func (s *store) patch(name string, upd *instancePatch) (*instanceModel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	m, ok := s.instances[name]
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "instance %s not found", name)
	}

	if upd.setDescription {
		m.description = upd.description
	}

	if upd.setLabels {
		m.labels = upd.labels
	}

	if upd.setFileShares {
		m.fileShares = upd.fileShares
	}

	return m.clone(), nil
}

func (s *store) delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.instances[name]; !ok {
		return cerrors.Newf(cerrors.NotFound, "instance %s not found", name)
	}

	delete(s.instances, name)

	return nil
}

// newOpID returns a unique operation id for a mutating request.
func (s *store) newOpID() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextOp++

	return "operation-" + strconv.Itoa(s.nextOp)
}

// assignIPs fills each network's output-only ipAddresses. Callers hold s.mu.
// A network whose reservedIpRange is a CIDR gets a host inside it; otherwise a
// deterministic private address is synthesized so the field is always populated
// (as real Filestore assigns one on create).
func (s *store) assignIPs(m *instanceModel) {
	for i := range m.networks {
		n := &m.networks[i]
		if len(n.ipAddresses) > 0 {
			continue
		}

		if ip, ok := hostInCIDR(n.reservedIPRange, ipHostOffset); ok {
			n.ipAddresses = []string{ip}
			continue
		}

		s.nextIP++
		n.ipAddresses = []string{ipFromUint32(privateIPBase + (s.nextIP & 0xFFFF))}
	}
}

// hostInCIDR returns the offset-th host address of cidr (e.g. offset 2 of
// 10.0.0.0/29 is 10.0.0.2). ok is false when cidr is not a valid IPv4 CIDR.
func hostInCIDR(cidr string, offset uint32) (string, bool) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", false
	}

	ip4 := ipnet.IP.To4()
	if ip4 == nil {
		return "", false
	}

	return ipFromUint32(binary.BigEndian.Uint32(ip4) + offset), true
}

// ipFromUint32 renders a big-endian uint32 as a dotted IPv4 string.
func ipFromUint32(v uint32) string {
	out := make(net.IP, net.IPv4len)
	binary.BigEndian.PutUint32(out, v)

	return out.String()
}
