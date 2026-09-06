package transfer

import "github.com/stackshy/cloudemu/v2/services/transfer/driver"

// The stored driver values carry slice, pointer, and map members, so every
// store read/write deep-copies to keep callers from aliasing internal state.

func copyStringSlice(in []string) []string {
	if in == nil {
		return nil
	}

	out := make([]string, len(in))
	copy(out, in)

	return out
}

func copyInt64Slice(in []int64) []int64 {
	if in == nil {
		return nil
	}

	out := make([]int64, len(in))
	copy(out, in)

	return out
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}

func copyInt64Ptr(n *int64) *int64 {
	if n == nil {
		return nil
	}

	v := *n

	return &v
}

func copyEndpointDetails(in *driver.EndpointDetails) *driver.EndpointDetails {
	if in == nil {
		return nil
	}

	return &driver.EndpointDetails{
		AddressAllocationIDs: copyStringSlice(in.AddressAllocationIDs),
		SubnetIDs:            copyStringSlice(in.SubnetIDs),
		VpcEndpointID:        in.VpcEndpointID,
		VpcID:                in.VpcID,
		SecurityGroupIDs:     copyStringSlice(in.SecurityGroupIDs),
	}
}

func copyPosixProfile(in *driver.PosixProfile) *driver.PosixProfile {
	if in == nil {
		return nil
	}

	return &driver.PosixProfile{
		UID:           copyInt64Ptr(in.UID),
		GID:           copyInt64Ptr(in.GID),
		SecondaryGIDs: copyInt64Slice(in.SecondaryGIDs),
	}
}

func copyHomeDirMappings(in []driver.HomeDirectoryMapEntry) []driver.HomeDirectoryMapEntry {
	if in == nil {
		return nil
	}

	out := make([]driver.HomeDirectoryMapEntry, len(in))
	copy(out, in)

	return out
}

func copySSHKeys(in []driver.SSHPublicKey) []driver.SSHPublicKey {
	if in == nil {
		return nil
	}

	out := make([]driver.SSHPublicKey, len(in))
	copy(out, in)

	return out
}

// copyServer deep-copies a server. Taken by value so it satisfies the
// func(V) V deep-copy callback signature used across the stores and snapshot.
//
//nolint:gocritic // hugeParam: value signature required by the func(V) V copy callback
func copyServer(in driver.Server) driver.Server {
	out := in
	out.EndpointDetails = copyEndpointDetails(in.EndpointDetails)
	out.IdentityProviderDetails = copyStringMap(in.IdentityProviderDetails)
	out.Protocols = copyStringSlice(in.Protocols)
	out.Tags = copyStringMap(in.Tags)

	return out
}

// copyUser deep-copies a user. Taken by value so it satisfies the func(V) V
// deep-copy callback signature used across the stores and snapshot.
//
//nolint:gocritic // hugeParam: value signature required by the func(V) V copy callback
func copyUser(in driver.User) driver.User {
	out := in
	out.HomeDirectoryMappings = copyHomeDirMappings(in.HomeDirectoryMappings)
	out.PosixProfile = copyPosixProfile(in.PosixProfile)
	out.SSHPublicKeys = copySSHKeys(in.SSHPublicKeys)
	out.Tags = copyStringMap(in.Tags)

	return out
}
