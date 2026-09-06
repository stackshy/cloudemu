package driver

import "time"

// Domain values (where a server stores files).
const (
	DomainS3  = "S3"
	DomainEFS = "EFS"
)

// EndpointType values.
const (
	EndpointTypePublic      = "PUBLIC"
	EndpointTypeVPC         = "VPC"
	EndpointTypeVPCEndpoint = "VPC_ENDPOINT"
)

// IdentityProviderType values.
const (
	IdentityProviderServiceManaged = "SERVICE_MANAGED"
	IdentityProviderAPIGateway     = "API_GATEWAY"
	IdentityProviderDirectory      = "AWS_DIRECTORY_SERVICE"
	IdentityProviderLambda         = "AWS_LAMBDA"
)

// Protocol values.
const (
	ProtocolSFTP = "SFTP"
	ProtocolFTP  = "FTP"
	ProtocolFTPS = "FTPS"
	ProtocolAS2  = "AS2"
)

// Server State values.
const (
	StateOffline     = "OFFLINE"
	StateOnline      = "ONLINE"
	StateStarting    = "STARTING"
	StateStopping    = "STOPPING"
	StateStartFailed = "START_FAILED"
	StateStopFailed  = "STOP_FAILED"
)

// HomeDirectoryType values.
const (
	HomeDirectoryPath    = "PATH"
	HomeDirectoryLogical = "LOGICAL"
)

// DefaultSecurityPolicyName is the security policy Transfer applies when a
// caller omits one.
const DefaultSecurityPolicyName = "TransferSecurityPolicy-2018-11"

// EndpointDetails carries the VPC/endpoint placement echoed back verbatim.
type EndpointDetails struct {
	AddressAllocationIDs []string
	SubnetIDs            []string
	VpcEndpointID        string
	VpcID                string
	SecurityGroupIDs     []string
}

// HomeDirectoryMapEntry maps a logical path onto a backing target (LOGICAL
// home-directory type only).
type HomeDirectoryMapEntry struct {
	Entry  string
	Target string
	Type   string
}

// PosixProfile is the POSIX identity applied to an EFS-backed user.
type PosixProfile struct {
	UID           *int64
	GID           *int64
	SecondaryGIDs []int64
}

// SSHPublicKey is a single SSH public key imported for a user.
type SSHPublicKey struct {
	DateImported     time.Time
	SSHPublicKeyBody string
	SSHPublicKeyID   string
}

// Server is a Transfer Family file-transfer server.
type Server struct {
	ServerID                string
	Arn                     string
	Domain                  string
	EndpointType            string
	EndpointDetails         *EndpointDetails
	HostKeyFingerprint      string
	IdentityProviderType    string
	IdentityProviderDetails map[string]string
	LoggingRole             string
	Protocols               []string
	SecurityPolicyName      string
	Certificate             string
	State                   string
	UserCount               int32
	Tags                    map[string]string
}

// ServerUpdate is the mutable-field delta applied by UpdateServer. A nil field
// is left unchanged.
type ServerUpdate struct {
	EndpointType            *string
	EndpointDetails         *EndpointDetails
	IdentityProviderDetails map[string]string
	LoggingRole             *string
	Protocols               []string
	SecurityPolicyName      *string
	Certificate             *string
}

// User is a user hosted on a SERVICE_MANAGED server.
type User struct {
	UserName              string
	Arn                   string
	ServerID              string
	Role                  string
	HomeDirectory         string
	HomeDirectoryType     string
	HomeDirectoryMappings []HomeDirectoryMapEntry
	Policy                string
	PosixProfile          *PosixProfile
	SSHPublicKeys         []SSHPublicKey
	Tags                  map[string]string
}

// UserUpdate is the mutable-field delta applied by UpdateUser.
type UserUpdate struct {
	Role                  *string
	HomeDirectory         *string
	HomeDirectoryType     *string
	HomeDirectoryMappings []HomeDirectoryMapEntry
	Policy                *string
	PosixProfile          *PosixProfile
}

// ListedServer is a server summary returned by ListServers.
type ListedServer struct {
	Arn                  string
	Domain               string
	IdentityProviderType string
	EndpointType         string
	LoggingRole          string
	ServerID             string
	State                string
	UserCount            int32
}

// ListedUser is a user summary returned by ListUsers.
type ListedUser struct {
	Arn               string
	HomeDirectory     string
	HomeDirectoryType string
	Role              string
	SSHPublicKeyCount int32
	UserName          string
}

// Pagination carries a next token and a max-results cap for list operations.
type Pagination struct {
	NextToken  string
	MaxResults int32
}
