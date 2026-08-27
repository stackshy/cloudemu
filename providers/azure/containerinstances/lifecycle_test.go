package containerinstances

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/containerinstances/driver"
)

// execEngine records exec calls so tests can assert the engine is driven.
type execEngine struct {
	recordingEngine
	execContainers []string
}

func (e *execEngine) Exec(_ context.Context, _, container string, _ []string) (config.ExecResult, error) {
	e.execContainers = append(e.execContainers, container)

	return config.ExecResult{Stdout: "ok"}, nil
}

func TestAssignsPublicIPAddress(t *testing.T) {
	m := New(config.NewOptions())

	cfg := sampleConfig()
	cfg.Location = "westus2"
	cfg.IPAddress = &driver.IPAddress{
		Type:         "Public",
		DNSNameLabel: "myapp",
		Ports:        []driver.Port{{Port: 80, Protocol: "TCP"}},
	}

	group, err := m.CreateContainerGroup(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if group.IPAddress == nil || group.IPAddress.IP == "" {
		t.Fatalf("public group got no assigned ip: %+v", group.IPAddress)
	}

	if group.IPAddress.FQDN != "myapp.westus2.azurecontainer.io" {
		t.Fatalf("fqdn = %q, want myapp.westus2.azurecontainer.io", group.IPAddress.FQDN)
	}
}

func TestPrivateIPAddressGetsNoFQDN(t *testing.T) {
	m := New(config.NewOptions())

	cfg := sampleConfig()
	cfg.IPAddress = &driver.IPAddress{Type: "Private"}

	group, err := m.CreateContainerGroup(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if group.IPAddress.IP != "" || group.IPAddress.FQDN != "" {
		t.Fatalf("private group should get no public ip/fqdn: %+v", group.IPAddress)
	}
}

func TestStopStartRestartStateTransitions(t *testing.T) {
	m := New(config.NewOptions())
	ctx := context.Background()

	if _, err := m.CreateContainerGroup(ctx, sampleConfig()); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := m.StopContainerGroup(ctx, testSub, testRG, "cg1"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	g, _ := m.GetContainerGroup(ctx, testSub, testRG, "cg1")
	if g.State != groupStateStopped || g.Containers[0].Current.State != containerStateTerminated {
		t.Fatalf("after stop: group=%q container=%q", g.State, g.Containers[0].Current.State)
	}

	if err := m.StartContainerGroup(ctx, testSub, testRG, "cg1"); err != nil {
		t.Fatalf("start: %v", err)
	}

	g, _ = m.GetContainerGroup(ctx, testSub, testRG, "cg1")
	if g.State != groupStateRunning {
		t.Fatalf("after start: group state = %q, want Running", g.State)
	}

	if err := m.RestartContainerGroup(ctx, testSub, testRG, "cg1"); err != nil {
		t.Fatalf("restart: %v", err)
	}

	// Missing group → NotFound for each verb.
	for _, err := range []error{
		m.StopContainerGroup(ctx, testSub, testRG, "ghost"),
		m.StartContainerGroup(ctx, testSub, testRG, "ghost"),
		m.RestartContainerGroup(ctx, testSub, testRG, "ghost"),
	} {
		if !cerrors.IsNotFound(err) {
			t.Fatalf("lifecycle on missing group = %v, want NotFound", err)
		}
	}
}

func TestExecValidatesAndDrivesEngine(t *testing.T) {
	eng := &execEngine{recordingEngine: recordingEngine{handle: "h1"}}
	m := New(config.NewOptions(config.WithContainerEngine(eng)))
	ctx := context.Background()

	if _, err := m.CreateContainerGroup(ctx, sampleConfig()); err != nil {
		t.Fatalf("create: %v", err)
	}

	session, err := m.ExecContainer(ctx, testSub, testRG, "cg1", "app", []string{"ls", "-la"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}

	if session.WebSocketURI == "" || session.Password == "" {
		t.Fatalf("exec session incomplete: %+v", session)
	}

	if len(eng.execContainers) != 1 || eng.execContainers[0] != "app" {
		t.Fatalf("engine exec not driven: %v", eng.execContainers)
	}

	// Unknown container → NotFound.
	if _, err := m.ExecContainer(ctx, testSub, testRG, "cg1", "ghost", []string{"ls"}); !cerrors.IsNotFound(err) {
		t.Fatalf("exec on missing container = %v, want NotFound", err)
	}

	// Unknown group → NotFound.
	if _, err := m.ExecContainer(ctx, testSub, testRG, "ghost", "app", []string{"ls"}); !cerrors.IsNotFound(err) {
		t.Fatalf("exec on missing group = %v, want NotFound", err)
	}
}
