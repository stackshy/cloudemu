package transfer

import "time"

// epochOrNil renders a time as a Unix-epoch float the Transfer SDK decodes into
// a *time.Time, or nil for the zero time.
func epochOrNil(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	secs := float64(t.Unix())

	return &secs
}

// --- nested wire shapes ---

type endpointDetailsJSON struct {
	AddressAllocationIDs []string `json:"AddressAllocationIds,omitempty"`
	SubnetIDs            []string `json:"SubnetIds,omitempty"`
	VpcEndpointID        string   `json:"VpcEndpointId,omitempty"`
	VpcID                string   `json:"VpcId,omitempty"`
	SecurityGroupIDs     []string `json:"SecurityGroupIds,omitempty"`
}

type homeDirMapEntryJSON struct {
	Entry  string `json:"Entry,omitempty"`
	Target string `json:"Target,omitempty"`
	Type   string `json:"Type,omitempty"`
}

type posixProfileJSON struct {
	UID           *int64  `json:"Uid,omitempty"`
	GID           *int64  `json:"Gid,omitempty"`
	SecondaryGIDs []int64 `json:"SecondaryGids,omitempty"`
}

type sshPublicKeyJSON struct {
	DateImported     *float64 `json:"DateImported,omitempty"`
	SSHPublicKeyBody string   `json:"SshPublicKeyBody,omitempty"`
	SSHPublicKeyID   string   `json:"SshPublicKeyId,omitempty"`
}

type tagJSON struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// describedServerJSON is the DescribedServer shape returned by DescribeServer.
type describedServerJSON struct {
	Arn                     string               `json:"Arn"`
	ServerID                string               `json:"ServerId"`
	Domain                  string               `json:"Domain,omitempty"`
	EndpointType            string               `json:"EndpointType,omitempty"`
	EndpointDetails         *endpointDetailsJSON `json:"EndpointDetails,omitempty"`
	HostKeyFingerprint      string               `json:"HostKeyFingerprint,omitempty"`
	IdentityProviderType    string               `json:"IdentityProviderType,omitempty"`
	IdentityProviderDetails map[string]string    `json:"IdentityProviderDetails,omitempty"`
	LoggingRole             string               `json:"LoggingRole,omitempty"`
	Protocols               []string             `json:"Protocols,omitempty"`
	SecurityPolicyName      string               `json:"SecurityPolicyName,omitempty"`
	Certificate             string               `json:"Certificate,omitempty"`
	State                   string               `json:"State,omitempty"`
	UserCount               int32                `json:"UserCount"`
	Tags                    []tagJSON            `json:"Tags,omitempty"`
}

// listedServerJSON is a server summary in the ListServers response.
type listedServerJSON struct {
	Arn                  string `json:"Arn"`
	Domain               string `json:"Domain,omitempty"`
	IdentityProviderType string `json:"IdentityProviderType,omitempty"`
	EndpointType         string `json:"EndpointType,omitempty"`
	LoggingRole          string `json:"LoggingRole,omitempty"`
	ServerID             string `json:"ServerId"`
	State                string `json:"State,omitempty"`
	UserCount            int32  `json:"UserCount"`
}

// describedUserJSON is the DescribedUser shape returned by DescribeUser.
type describedUserJSON struct {
	Arn                   string                `json:"Arn"`
	UserName              string                `json:"UserName"`
	Role                  string                `json:"Role,omitempty"`
	HomeDirectory         string                `json:"HomeDirectory,omitempty"`
	HomeDirectoryType     string                `json:"HomeDirectoryType,omitempty"`
	HomeDirectoryMappings []homeDirMapEntryJSON `json:"HomeDirectoryMappings,omitempty"`
	Policy                string                `json:"Policy,omitempty"`
	PosixProfile          *posixProfileJSON     `json:"PosixProfile,omitempty"`
	SSHPublicKeys         []sshPublicKeyJSON    `json:"SshPublicKeys,omitempty"`
	Tags                  []tagJSON             `json:"Tags,omitempty"`
}

// listedUserJSON is a user summary in the ListUsers response.
type listedUserJSON struct {
	Arn               string `json:"Arn"`
	HomeDirectory     string `json:"HomeDirectory,omitempty"`
	HomeDirectoryType string `json:"HomeDirectoryType,omitempty"`
	Role              string `json:"Role,omitempty"`
	SSHPublicKeyCount int32  `json:"SshPublicKeyCount"`
	UserName          string `json:"UserName"`
}
