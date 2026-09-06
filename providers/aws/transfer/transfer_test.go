package transfer

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/services/transfer/driver"
)

func newMock(t *testing.T) *Mock {
	t.Helper()

	opts := config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(1_700_000_000, 0))))

	return New(opts)
}

func requireNoError(t *testing.T, err error, msg string) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
}

func ptr[T any](v T) *T { return &v }

var (
	serverIDRe = regexp.MustCompile(`^s-[0-9a-f]{17}$`)
	keyIDRe    = regexp.MustCompile(`^key-[0-9a-f]{17}$`)
)

func TestCreateServerDefaultsAndFormats(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	id, err := m.CreateServer(ctx, driver.Server{})
	requireNoError(t, err, "CreateServer")

	if !serverIDRe.MatchString(id) {
		t.Fatalf("ServerID %q does not match s-<17hex>", id)
	}

	s, err := m.DescribeServer(ctx, id)
	requireNoError(t, err, "DescribeServer")

	if s.Domain != driver.DomainS3 {
		t.Fatalf("Domain = %q, want S3", s.Domain)
	}

	if s.EndpointType != driver.EndpointTypePublic {
		t.Fatalf("EndpointType = %q, want PUBLIC", s.EndpointType)
	}

	if s.IdentityProviderType != driver.IdentityProviderServiceManaged {
		t.Fatalf("IdentityProviderType = %q, want SERVICE_MANAGED", s.IdentityProviderType)
	}

	if len(s.Protocols) != 1 || s.Protocols[0] != driver.ProtocolSFTP {
		t.Fatalf("Protocols = %v, want [SFTP]", s.Protocols)
	}

	if s.SecurityPolicyName != driver.DefaultSecurityPolicyName {
		t.Fatalf("SecurityPolicyName = %q", s.SecurityPolicyName)
	}

	if s.State != driver.StateOnline {
		t.Fatalf("State = %q, want ONLINE (synchronous)", s.State)
	}

	if s.HostKeyFingerprint == "" {
		t.Fatalf("HostKeyFingerprint is empty")
	}

	wantArn := "arn:aws:transfer:us-east-1:123456789012:server/" + id
	if s.Arn != wantArn {
		t.Fatalf("Arn = %q, want %q", s.Arn, wantArn)
	}

	if s.UserCount != 0 {
		t.Fatalf("UserCount = %d, want 0", s.UserCount)
	}
}

func TestCreateServerEchoesProtocolsAndEndpointDetails(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	id, err := m.CreateServer(ctx, driver.Server{
		Protocols:    []string{driver.ProtocolSFTP, driver.ProtocolFTPS},
		EndpointType: driver.EndpointTypeVPC,
		EndpointDetails: &driver.EndpointDetails{
			VpcID:     "vpc-123",
			SubnetIDs: []string{"subnet-a", "subnet-b"},
		},
	})
	requireNoError(t, err, "CreateServer")

	s, err := m.DescribeServer(ctx, id)
	requireNoError(t, err, "DescribeServer")

	if len(s.Protocols) != 2 || s.Protocols[1] != driver.ProtocolFTPS {
		t.Fatalf("Protocols not round-tripped: %v", s.Protocols)
	}

	if s.EndpointDetails == nil || s.EndpointDetails.VpcID != "vpc-123" || len(s.EndpointDetails.SubnetIDs) != 2 {
		t.Fatalf("EndpointDetails not round-tripped: %+v", s.EndpointDetails)
	}
}

func TestCreateServerRejectsInvalidProtocol(t *testing.T) {
	m := newMock(t)

	if _, err := m.CreateServer(context.Background(), driver.Server{Protocols: []string{"GARBAGE"}}); err == nil {
		t.Fatalf("expected invalid-protocol error")
	}
}

func TestStartStopServer(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	id, err := m.CreateServer(ctx, driver.Server{})
	requireNoError(t, err, "CreateServer")

	requireNoError(t, m.StopServer(ctx, id), "StopServer")

	s, _ := m.DescribeServer(ctx, id)
	if s.State != driver.StateOffline {
		t.Fatalf("State after stop = %q, want OFFLINE", s.State)
	}

	requireNoError(t, m.StartServer(ctx, id), "StartServer")

	s, _ = m.DescribeServer(ctx, id)
	if s.State != driver.StateOnline {
		t.Fatalf("State after start = %q, want ONLINE", s.State)
	}
}

func TestUserLifecycleAndUserCount(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	id, err := m.CreateServer(ctx, driver.Server{})
	requireNoError(t, err, "CreateServer")

	err = m.CreateUser(ctx, driver.User{
		ServerID:      id,
		UserName:      "alice",
		Role:          "arn:aws:iam::123456789012:role/transfer",
		HomeDirectory: "/bucket/alice",
	})
	requireNoError(t, err, "CreateUser")

	u, err := m.DescribeUser(ctx, id, "alice")
	requireNoError(t, err, "DescribeUser")

	if u.HomeDirectoryType != driver.HomeDirectoryPath {
		t.Fatalf("HomeDirectoryType = %q, want PATH", u.HomeDirectoryType)
	}

	wantArn := "arn:aws:transfer:us-east-1:123456789012:user/" + id + "/alice"
	if u.Arn != wantArn {
		t.Fatalf("user Arn = %q, want %q", u.Arn, wantArn)
	}

	s, _ := m.DescribeServer(ctx, id)
	if s.UserCount != 1 {
		t.Fatalf("UserCount after create = %d, want 1", s.UserCount)
	}

	requireNoError(t, m.DeleteUser(ctx, id, "alice"), "DeleteUser")

	s, _ = m.DescribeServer(ctx, id)
	if s.UserCount != 0 {
		t.Fatalf("UserCount after delete = %d, want 0", s.UserCount)
	}
}

func TestCreateUserDuplicateRejected(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	id, _ := m.CreateServer(ctx, driver.Server{})
	requireNoError(t, m.CreateUser(ctx, driver.User{ServerID: id, UserName: "bob"}), "CreateUser")

	if err := m.CreateUser(ctx, driver.User{ServerID: id, UserName: "bob"}); err == nil {
		t.Fatalf("expected duplicate-user error")
	}
}

func TestCreateUserUnknownServer(t *testing.T) {
	m := newMock(t)

	if err := m.CreateUser(context.Background(), driver.User{ServerID: "s-00000000000000000", UserName: "x"}); err == nil {
		t.Fatalf("expected not-found error for unknown server")
	}
}

func TestImportAndDeleteSSHPublicKey(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	id, _ := m.CreateServer(ctx, driver.Server{})
	requireNoError(t, m.CreateUser(ctx, driver.User{ServerID: id, UserName: "carol"}), "CreateUser")

	keyID, err := m.ImportSSHPublicKey(ctx, id, "carol", "ssh-rsa AAAAB3...")
	requireNoError(t, err, "ImportSSHPublicKey")

	if !keyIDRe.MatchString(keyID) {
		t.Fatalf("SSHPublicKeyID %q does not match key-<17hex>", keyID)
	}

	u, _ := m.DescribeUser(ctx, id, "carol")
	if len(u.SSHPublicKeys) != 1 {
		t.Fatalf("SSHPublicKeyCount = %d, want 1", len(u.SSHPublicKeys))
	}

	if u.SSHPublicKeys[0].DateImported.IsZero() {
		t.Fatalf("DateImported is zero")
	}

	requireNoError(t, m.DeleteSSHPublicKey(ctx, id, "carol", keyID), "DeleteSSHPublicKey")

	u, _ = m.DescribeUser(ctx, id, "carol")
	if len(u.SSHPublicKeys) != 0 {
		t.Fatalf("SSHPublicKeyCount after delete = %d, want 0", len(u.SSHPublicKeys))
	}
}

func TestImportSSHPublicKeyCap(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	id, _ := m.CreateServer(ctx, driver.Server{})
	requireNoError(t, m.CreateUser(ctx, driver.User{ServerID: id, UserName: "dan"}), "CreateUser")

	for i := 0; i < maxSSHKeysPerUser; i++ {
		if _, err := m.ImportSSHPublicKey(ctx, id, "dan", "ssh-rsa key"); err != nil {
			t.Fatalf("import %d: %v", i, err)
		}
	}

	if _, err := m.ImportSSHPublicKey(ctx, id, "dan", "ssh-rsa overflow"); err == nil {
		t.Fatalf("expected cap error on the 6th key")
	}
}

func TestUpdateServerAndUser(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	id, _ := m.CreateServer(ctx, driver.Server{})

	err := m.UpdateServer(ctx, id, driver.ServerUpdate{
		Protocols:          []string{driver.ProtocolSFTP, driver.ProtocolFTP},
		SecurityPolicyName: ptr("TransferSecurityPolicy-2020-06"),
	})
	requireNoError(t, err, "UpdateServer")

	s, _ := m.DescribeServer(ctx, id)
	if len(s.Protocols) != 2 || s.SecurityPolicyName != "TransferSecurityPolicy-2020-06" {
		t.Fatalf("server update not applied: %+v", s)
	}

	requireNoError(t, m.CreateUser(ctx, driver.User{ServerID: id, UserName: "erin"}), "CreateUser")
	requireNoError(t, m.UpdateUser(ctx, id, "erin", driver.UserUpdate{HomeDirectory: ptr("/new/home")}), "UpdateUser")

	u, _ := m.DescribeUser(ctx, id, "erin")
	if u.HomeDirectory != "/new/home" {
		t.Fatalf("user HomeDirectory = %q", u.HomeDirectory)
	}
}

func TestDeleteServerCascadesUsers(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	id, _ := m.CreateServer(ctx, driver.Server{})
	requireNoError(t, m.CreateUser(ctx, driver.User{ServerID: id, UserName: "frank"}), "CreateUser")
	requireNoError(t, m.DeleteServer(ctx, id), "DeleteServer")

	if _, err := m.DescribeUser(ctx, id, "frank"); err == nil {
		t.Fatalf("expected user to be gone after server delete")
	}

	if _, err := m.DescribeServer(ctx, id); err == nil {
		t.Fatalf("expected server to be gone")
	}
}

func TestListServersAndUsersDeterministic(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	id, _ := m.CreateServer(ctx, driver.Server{})

	for _, name := range []string{"zoe", "amy", "mia"} {
		requireNoError(t, m.CreateUser(ctx, driver.User{ServerID: id, UserName: name}), "CreateUser")
	}

	users, _, err := m.ListUsers(ctx, id, driver.Pagination{})
	requireNoError(t, err, "ListUsers")

	if len(users) != 3 || users[0].UserName != "amy" || users[2].UserName != "zoe" {
		t.Fatalf("ListUsers not sorted: %+v", users)
	}

	servers, _, err := m.ListServers(ctx, driver.Pagination{})
	requireNoError(t, err, "ListServers")

	if len(servers) != 1 || servers[0].UserCount != 3 {
		t.Fatalf("ListServers unexpected: %+v", servers)
	}
}

func TestTagResourceRoundTrip(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	id, _ := m.CreateServer(ctx, driver.Server{Tags: map[string]string{"env": "prod"}})
	arn := m.serverARN(id)

	tags, err := m.ListTagsForResource(ctx, arn)
	requireNoError(t, err, "ListTagsForResource")

	if tags["env"] != "prod" {
		t.Fatalf("create tags not stored: %+v", tags)
	}

	requireNoError(t, m.TagResource(ctx, arn, map[string]string{"team": "data"}), "TagResource")
	requireNoError(t, m.UntagResource(ctx, arn, []string{"env"}), "UntagResource")

	tags, _ = m.ListTagsForResource(ctx, arn)
	if tags["team"] != "data" || tags["env"] != "" {
		t.Fatalf("tag update not applied: %+v", tags)
	}
}
