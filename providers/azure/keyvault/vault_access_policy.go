package keyvault

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// UpdateVault atomically applies mutate to a clone of the stored vault (ARM
// PATCH) and re-applies the server-side defaults, so a PATCH can never revert
// soft delete. If mutate fails the stored vault is left unchanged.
func (m *Mock) UpdateVault(_ context.Context, name string,
	mutate func(*driver.KVVaultInfo) error,
) (*driver.KVVaultInfo, error) {
	var (
		out       *driver.KVVaultInfo
		mutateErr error
	)

	found := m.armVaults.Update(name, func(stored *driver.KVVaultInfo) *driver.KVVaultInfo {
		clone := cloneVaultInfo(stored)
		if mutateErr = mutate(clone); mutateErr != nil {
			return stored
		}

		clone.Name = stored.Name
		if clone.Properties.VaultURI == "" {
			clone.Properties.VaultURI = stored.Properties.VaultURI
		}

		applyVaultPropertyDefaults(&clone.Properties)
		out = cloneVaultInfo(clone)

		return clone
	})
	if !found {
		return nil, errors.Newf(errors.NotFound, "vault %q not found", name)
	}

	if mutateErr != nil {
		return nil, mutateErr
	}

	return out, nil
}

// UpdateVaultAccessPolicies atomically applies an accessPolicies/{kind}
// update. Only the access-policy list changes; the vault's other properties
// are not touched and the defaults are not re-applied.
func (m *Mock) UpdateVaultAccessPolicies(_ context.Context, name string, kind driver.KVAccessPolicyUpdateKind,
	entries []driver.KVAccessPolicy,
) ([]driver.KVAccessPolicy, error) {
	switch kind {
	case driver.KVAccessPolicyAdd, driver.KVAccessPolicyReplace, driver.KVAccessPolicyRemove:
	default:
		return nil, errors.Newf(errors.InvalidArgument, "unknown access policy update kind %q", kind)
	}

	var (
		out      []driver.KVAccessPolicy
		applyErr error
	)

	found := m.armVaults.Update(name, func(stored *driver.KVVaultInfo) *driver.KVVaultInfo {
		clone := cloneVaultInfo(stored)
		for i := range entries {
			clone.Properties.AccessPolicies, applyErr = applyAccessPolicy(clone.Properties.AccessPolicies, kind, &entries[i])
			if applyErr != nil {
				return stored
			}
		}

		out = copyAccessPolicies(clone.Properties.AccessPolicies)

		return clone
	})
	if !found {
		return nil, errors.Newf(errors.NotFound, "vault %q not found", name)
	}

	if applyErr != nil {
		return nil, applyErr
	}

	if out == nil {
		out = []driver.KVAccessPolicy{}
	}

	return out, nil
}

// applyAccessPolicy applies one entry to list. add unions permissions and
// appends a new principal; replace overwrites the permissions of an existing
// principal and is NotFound for an absent one; remove subtracts permissions,
// drops an entry left with none, and drops the whole entry when the request
// names no permissions (removal by identity, as az keyvault delete-policy
// sends it).
func applyAccessPolicy(list []driver.KVAccessPolicy, kind driver.KVAccessPolicyUpdateKind,
	entry *driver.KVAccessPolicy,
) ([]driver.KVAccessPolicy, error) {
	idx := -1

	for i := range list {
		if sameIdentity(&list[i], entry) {
			idx = i
			break
		}
	}

	if idx < 0 {
		switch kind {
		case driver.KVAccessPolicyRemove:
			return list, nil
		case driver.KVAccessPolicyReplace:
			return nil, errors.Newf(errors.NotFound,
				"no access policy for object %q to replace", entry.ObjectID)
		case driver.KVAccessPolicyAdd: // appends the new principal below
		}

		return append(list, copyAccessPolicies([]driver.KVAccessPolicy{*entry})...), nil
	}

	p := &list[idx].Permissions
	e := &entry.Permissions

	switch kind {
	case driver.KVAccessPolicyAdd:
		p.Keys, p.Secrets = unionFold(p.Keys, e.Keys), unionFold(p.Secrets, e.Secrets)
		p.Certificates, p.Storage = unionFold(p.Certificates, e.Certificates), unionFold(p.Storage, e.Storage)
	case driver.KVAccessPolicyReplace:
		*p = copyAccessPolicies([]driver.KVAccessPolicy{*entry})[0].Permissions
	case driver.KVAccessPolicyRemove:
		if permissionCount(e) == 0 {
			return append(list[:idx], list[idx+1:]...), nil
		}

		p.Keys, p.Secrets = minusFold(p.Keys, e.Keys), minusFold(p.Secrets, e.Secrets)
		p.Certificates, p.Storage = minusFold(p.Certificates, e.Certificates), minusFold(p.Storage, e.Storage)

		if permissionCount(p) == 0 {
			return append(list[:idx], list[idx+1:]...), nil
		}
	}

	return list, nil
}

func permissionCount(p *driver.KVAccessPermissions) int {
	return len(p.Keys) + len(p.Secrets) + len(p.Certificates) + len(p.Storage)
}

func sameIdentity(a, b *driver.KVAccessPolicy) bool {
	return strings.EqualFold(a.TenantID, b.TenantID) && strings.EqualFold(a.ObjectID, b.ObjectID) &&
		strings.EqualFold(a.ApplicationID, b.ApplicationID)
}

// unionFold appends the values of add not already in base, compared
// case-insensitively, keeping base's order and case.
func unionFold(base, add []string) []string {
	out := copyStrings(base)

	for _, v := range add {
		if !containsFold(out, v) {
			out = append(out, v)
		}
	}

	return out
}

// minusFold returns base without the values in remove, compared
// case-insensitively.
func minusFold(base, remove []string) []string {
	out := make([]string, 0, len(base))

	for _, v := range base {
		if !containsFold(remove, v) {
			out = append(out, v)
		}
	}

	return out
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}

	return false
}
