// Package driver defines the interface and types for AWS Transfer Family
// implementations. It models the file-transfer control plane: servers (the
// SFTP/FTPS/FTP/AS2 endpoints), the users hosted on a SERVICE_MANAGED server,
// and the SSH public keys imported for those users, plus resource tagging.
//
// This is a control-plane emulator only: there is no real SFTP/FTPS data plane
// behind it, so no files are ever transferred. A created server settles to
// State ONLINE synchronously (the Terraform aws_transfer_server resource treats
// state as Computed and does not wait on it), and StartServer/StopServer flip
// the state directly.
package driver

import "context"

// Transfer is the interface an AWS Transfer Family backend implements.
type Transfer interface {
	serverAPI
	userAPI
	sshKeyAPI
	tagAPI
}

// serverAPI covers the file-transfer server control plane.
type serverAPI interface {
	// CreateServer creates a server, materializing the real Transfer defaults
	// (Domain S3, EndpointType PUBLIC, IdentityProviderType SERVICE_MANAGED,
	// Protocols [SFTP], SecurityPolicyName default, State ONLINE) and returns its
	// generated ServerID ("s-<17hex>").
	CreateServer(ctx context.Context, in Server) (string, error)
	// DescribeServer returns a deep copy of a server, or a
	// ResourceNotFoundException-tagged error when it does not exist.
	DescribeServer(ctx context.Context, serverID string) (*Server, error)
	// UpdateServer applies the mutable-field delta to a server. A nil field is
	// left unchanged; a supplied field replaces the stored value.
	UpdateServer(ctx context.Context, serverID string, upd ServerUpdate) error
	// DeleteServer removes a server and every user hosted on it.
	DeleteServer(ctx context.Context, serverID string) error
	// ListServers returns server summaries in a deterministic (ServerID) order.
	ListServers(ctx context.Context, page Pagination) ([]ListedServer, string, error)
	// StartServer transitions a server to State ONLINE.
	StartServer(ctx context.Context, serverID string) error
	// StopServer transitions a server to State OFFLINE.
	StopServer(ctx context.Context, serverID string) error
}

// userAPI covers the users hosted on a SERVICE_MANAGED server.
type userAPI interface {
	// CreateUser creates a user on a server and increments the server's
	// UserCount. UserName is unique per server.
	CreateUser(ctx context.Context, in User) error
	// DescribeUser returns a deep copy of a user (including its imported SSH
	// public keys), or a ResourceNotFoundException-tagged error when absent.
	DescribeUser(ctx context.Context, serverID, userName string) (*User, error)
	// UpdateUser applies the mutable-field delta to a user.
	UpdateUser(ctx context.Context, serverID, userName string, upd UserUpdate) error
	// DeleteUser removes a user and decrements the server's UserCount.
	DeleteUser(ctx context.Context, serverID, userName string) error
	// ListUsers returns user summaries for a server in a deterministic
	// (UserName) order.
	ListUsers(ctx context.Context, serverID string, page Pagination) ([]ListedUser, string, error)
}

// sshKeyAPI covers the SSH public keys imported for a user.
type sshKeyAPI interface {
	// ImportSSHPublicKey appends a key to a user and returns its generated
	// SSHPublicKeyID ("key-<17hex>"), incrementing the user's key count.
	ImportSSHPublicKey(ctx context.Context, serverID, userName, body string) (string, error)
	// DeleteSSHPublicKey removes a key from a user by its SSHPublicKeyID.
	DeleteSSHPublicKey(ctx context.Context, serverID, userName, keyID string) error
}

// tagAPI covers resource tagging, keyed by the server or user ARN.
type tagAPI interface {
	TagResource(ctx context.Context, arn string, tags map[string]string) error
	UntagResource(ctx context.Context, arn string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, arn string) (map[string]string, error)
}
