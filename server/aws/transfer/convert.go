package transfer

import (
	"sort"

	"github.com/stackshy/cloudemu/v2/services/transfer/driver"
)

// --- wire -> driver ---

func endpointDetailsFromWire(in *endpointDetailsJSON) *driver.EndpointDetails {
	if in == nil {
		return nil
	}

	return &driver.EndpointDetails{
		AddressAllocationIDs: in.AddressAllocationIDs,
		SubnetIDs:            in.SubnetIDs,
		VpcEndpointID:        in.VpcEndpointID,
		VpcID:                in.VpcID,
		SecurityGroupIDs:     in.SecurityGroupIDs,
	}
}

func posixProfileFromWire(in *posixProfileJSON) *driver.PosixProfile {
	if in == nil {
		return nil
	}

	return &driver.PosixProfile{UID: in.UID, GID: in.GID, SecondaryGIDs: in.SecondaryGIDs}
}

func homeDirMappingsFromWire(in []homeDirMapEntryJSON) []driver.HomeDirectoryMapEntry {
	if in == nil {
		return nil
	}

	out := make([]driver.HomeDirectoryMapEntry, 0, len(in))
	for _, e := range in {
		out = append(out, driver.HomeDirectoryMapEntry{Entry: e.Entry, Target: e.Target, Type: e.Type})
	}

	return out
}

// tagsToMap folds a wire tag list into a map (last value wins on a repeated key).
func tagsToMap(in []tagJSON) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for _, t := range in {
		out[t.Key] = t.Value
	}

	return out
}

// --- driver -> wire ---

func endpointDetailsToWire(in *driver.EndpointDetails) *endpointDetailsJSON {
	if in == nil {
		return nil
	}

	return &endpointDetailsJSON{
		AddressAllocationIDs: in.AddressAllocationIDs,
		SubnetIDs:            in.SubnetIDs,
		VpcEndpointID:        in.VpcEndpointID,
		VpcID:                in.VpcID,
		SecurityGroupIDs:     in.SecurityGroupIDs,
	}
}

func posixProfileToWire(in *driver.PosixProfile) *posixProfileJSON {
	if in == nil {
		return nil
	}

	return &posixProfileJSON{UID: in.UID, GID: in.GID, SecondaryGIDs: in.SecondaryGIDs}
}

func homeDirMappingsToWire(in []driver.HomeDirectoryMapEntry) []homeDirMapEntryJSON {
	if len(in) == 0 {
		return nil
	}

	out := make([]homeDirMapEntryJSON, 0, len(in))
	for _, e := range in {
		out = append(out, homeDirMapEntryJSON{Entry: e.Entry, Target: e.Target, Type: e.Type})
	}

	return out
}

func sshKeysToWire(in []driver.SSHPublicKey) []sshPublicKeyJSON {
	if len(in) == 0 {
		return nil
	}

	out := make([]sshPublicKeyJSON, 0, len(in))
	for _, k := range in {
		out = append(out, sshPublicKeyJSON{
			DateImported:     epochOrNil(k.DateImported),
			SSHPublicKeyBody: k.SSHPublicKeyBody,
			SSHPublicKeyID:   k.SSHPublicKeyID,
		})
	}

	return out
}

// mapToTags renders a tag map as a sorted wire tag list (deterministic order).
func mapToTags(in map[string]string) []tagJSON {
	if len(in) == 0 {
		return nil
	}

	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := make([]tagJSON, 0, len(keys))
	for _, k := range keys {
		out = append(out, tagJSON{Key: k, Value: in[k]})
	}

	return out
}

func serverToWire(s *driver.Server) describedServerJSON {
	return describedServerJSON{
		Arn:                     s.Arn,
		ServerID:                s.ServerID,
		Domain:                  s.Domain,
		EndpointType:            s.EndpointType,
		EndpointDetails:         endpointDetailsToWire(s.EndpointDetails),
		HostKeyFingerprint:      s.HostKeyFingerprint,
		IdentityProviderType:    s.IdentityProviderType,
		IdentityProviderDetails: s.IdentityProviderDetails,
		LoggingRole:             s.LoggingRole,
		Protocols:               s.Protocols,
		SecurityPolicyName:      s.SecurityPolicyName,
		Certificate:             s.Certificate,
		State:                   s.State,
		UserCount:               s.UserCount,
		Tags:                    mapToTags(s.Tags),
	}
}

func listedServerToWire(s *driver.ListedServer) listedServerJSON {
	return listedServerJSON{
		Arn:                  s.Arn,
		Domain:               s.Domain,
		IdentityProviderType: s.IdentityProviderType,
		EndpointType:         s.EndpointType,
		LoggingRole:          s.LoggingRole,
		ServerID:             s.ServerID,
		State:                s.State,
		UserCount:            s.UserCount,
	}
}

func userToWire(u *driver.User) describedUserJSON {
	return describedUserJSON{
		Arn:                   u.Arn,
		UserName:              u.UserName,
		Role:                  u.Role,
		HomeDirectory:         u.HomeDirectory,
		HomeDirectoryType:     u.HomeDirectoryType,
		HomeDirectoryMappings: homeDirMappingsToWire(u.HomeDirectoryMappings),
		Policy:                u.Policy,
		PosixProfile:          posixProfileToWire(u.PosixProfile),
		SSHPublicKeys:         sshKeysToWire(u.SSHPublicKeys),
		Tags:                  mapToTags(u.Tags),
	}
}

func listedUserToWire(u *driver.ListedUser) listedUserJSON {
	return listedUserJSON{
		Arn:               u.Arn,
		HomeDirectory:     u.HomeDirectory,
		HomeDirectoryType: u.HomeDirectoryType,
		Role:              u.Role,
		SSHPublicKeyCount: u.SSHPublicKeyCount,
		UserName:          u.UserName,
	}
}
