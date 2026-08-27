package containerinstances

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/container/containerengine"
	"github.com/stackshy/cloudemu/v2/services/containerinstances/driver"
)

const (
	// aciDNSSuffix is the DNS zone ACI assigns public container-group FQDNs under.
	aciDNSSuffix = "azurecontainer.io"
	// defaultIPLocation is used when a Public group is created without a location.
	defaultIPLocation = "eastus"

	octetMask     = 0xFF
	octetCount    = 4
	octetShift    = 8
	passwordBytes = 16
)

// assignIPAddress echoes back the requested IP configuration and, for a Public
// group with a DNS name label, assigns a public IP and computes the FQDN Azure
// would hand out (<label>.<location>.azurecontainer.io).
func assignIPAddress(in *driver.IPAddress, location string) *driver.IPAddress {
	if in == nil {
		return nil
	}

	out := *in
	out.Ports = append([]driver.Port(nil), in.Ports...)

	if !strings.EqualFold(out.Type, "Public") {
		return &out
	}

	if out.IP == "" {
		out.IP = synthPublicIP(out.DNSNameLabel + location)
	}

	if out.DNSNameLabel != "" && out.FQDN == "" {
		loc := location
		if loc == "" {
			loc = defaultIPLocation
		}

		out.FQDN = out.DNSNameLabel + "." + loc + "." + aciDNSSuffix
	}

	return &out
}

// synthPublicIP derives a stable, non-routable-looking public IP from seed so a
// group keeps the same address across reads.
func synthPublicIP(seed string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(seed))
	v := h.Sum32()

	octets := make([]string, octetCount)
	for i := 0; i < octetCount; i++ {
		octets[i] = fmt.Sprintf("%d", (v>>(octetShift*uint(i)))&octetMask)
	}

	// Keep the leading octet away from 0 so it reads as a real address.
	if octets[0] == "0" {
		octets[0] = "20"
	}

	return strings.Join(octets, ".")
}

// StartContainerGroup restarts the group's workload and returns it to Running.
func (m *Mock) StartContainerGroup(ctx context.Context, subscription, resourceGroup, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := groupKey(subscription, resourceGroup, name)

	if !m.groups.Has(key) {
		return cerrors.Newf(cerrors.NotFound, "container group %q not found", name)
	}

	return m.runGroup(ctx, key)
}

// StopContainerGroup tears down the workload and marks the group Stopped.
func (m *Mock) StopContainerGroup(ctx context.Context, subscription, resourceGroup, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := groupKey(subscription, resourceGroup, name)

	data, ok := m.groups.Get(key)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container group %q not found", name)
	}

	m.stopWorkload(ctx, data)

	m.groups.Update(key, func(d *groupData) *groupData {
		d.engineBacked = false
		d.handle = ""
		d.group.State = groupStateStopped

		for i := range d.group.Containers {
			d.group.Containers[i].Current = driver.ContainerState{State: containerStateTerminated}
		}

		return d
	})

	return nil
}

// RestartContainerGroup tears the workload down and runs it again.
func (m *Mock) RestartContainerGroup(ctx context.Context, subscription, resourceGroup, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := groupKey(subscription, resourceGroup, name)

	data, ok := m.groups.Get(key)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "container group %q not found", name)
	}

	m.stopWorkload(ctx, data)

	return m.runGroup(ctx, key)
}

// runGroup resets the group at key to a fresh Running state and re-runs it on
// the engine (a no-op when none is configured), reflecting the observed state
// back.
func (m *Mock) runGroup(ctx context.Context, key string) error {
	var runErr error

	m.groups.Update(key, func(data *groupData) *groupData {
		cfg := driver.ContainerGroupConfig{
			Name:          data.group.Name,
			RestartPolicy: data.group.RestartPolicy,
			Containers:    configsFromInstances(data.group.Containers),
			Scope:         data.group.Scope,
		}

		data.group.State = groupStateRunning
		data.group.Containers = synthContainers(cfg.Containers)
		data.engineBacked = false
		data.handle = ""

		runErr = m.backWithEngine(ctx, &cfg, data)

		return data
	})

	return runErr
}

// ExecContainer opens an exec session on one container. When the group is
// engine-backed the command runs for real on the engine before the session
// descriptor is returned.
func (m *Mock) ExecContainer(
	ctx context.Context, subscription, resourceGroup, group, container string, command []string,
) (*driver.ExecSession, error) {
	data, ok := m.groups.Get(groupKey(subscription, resourceGroup, group))
	if !ok {
		return nil, cerrors.Newf(cerrors.NotFound, "container group %q not found", group)
	}

	if !containerExists(&data.group, container) {
		return nil, cerrors.Newf(cerrors.NotFound, "container %q not found in group %q", container, group)
	}

	if data.engineBacked {
		if _, err := containerengine.Exec(ctx, m.opts.ContainerEngine, data.handle, container, command); err != nil {
			return nil, cerrors.Newf(cerrors.Internal, "exec in container %q: %v", container, err)
		}
	}

	loc := data.group.Location
	if loc == "" {
		loc = defaultIPLocation
	}

	return &driver.ExecSession{
		WebSocketURI: "wss://" + loc + "." + aciDNSSuffix + "/exec/" + group + "/" + container,
		Password:     randomToken(),
	}, nil
}

// containerExists reports whether the group holds a container of the given name.
func containerExists(g *driver.ContainerGroup, name string) bool {
	for i := range g.Containers {
		if g.Containers[i].Name == name {
			return true
		}
	}

	return false
}

// configsFromInstances rebuilds the create-time container configs from a group's
// recorded instances, so a stopped group can be run again.
func configsFromInstances(in []driver.ContainerInstance) []driver.ContainerConfig {
	out := make([]driver.ContainerConfig, 0, len(in))

	for i := range in {
		c := &in[i]
		out = append(out, driver.ContainerConfig{
			Name:       c.Name,
			Image:      c.Image,
			Command:    append([]string(nil), c.Command...),
			CPU:        c.CPU,
			MemoryInGB: c.MemoryInGB,
			Env:        append([]driver.EnvVar(nil), c.Env...),
		})
	}

	return out
}

// randomToken returns a hex secret for an exec session password.
func randomToken() string {
	buf := make([]byte, passwordBytes)
	if _, err := rand.Read(buf); err != nil {
		return "cloudemu-exec-password"
	}

	return hex.EncodeToString(buf)
}
