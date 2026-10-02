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

	var out []driver.KVAccessPolicy

	found := m.armVaults.Update(name, func(stored *driver.KVVaultInfo) *driver.KVVaultInfo {
		clone := cloneVaultInfo(stored)
		for i := range entries {
			clone.Properties.AccessPolicies = applyAccessPolicy(clone.Properties.AccessPolicies, kind, &entries[i])
		}

		out = copyAccessPolicies(clone.Properties.AccessPolicies)

		return clone
	})
	if !found {
		return nil, errors.Newf(errors.NotFound, "vault %q not found", name)
	}

	if out == nil {
		out = []driver.KVAccessPolicy{}
	}

	return out, nil
}

// applyAccessPolicy applies one entry to list. add unions permissions,
// replace overwrites them (both append when no entry matches), and remove
// subtracts them and drops an entry left with no permissions.
func applyAccessPolicy(list []driver.KVAccessPolicy, kind driver.KVAccessPolicyUpdateKind,
	entry *driver.KVAccessPolicy,
) []driver.KVAccessPolicy {
	idx := -1

	for i := range list {
		if sameIdentity(&list[i], entry) {
			idx = i
			break
		}
	}

	if idx < 0 {
		if kind == driver.KVAccessPolicyRemove {
			return list
		}

		return append(list, copyAccessPolicies([]driver.KVAccessPolicy{*entry})...)
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
		p.Keys, p.Secrets = minusFold(p.Keys, e.Keys), minusFold(p.Secrets, e.Secrets)
		p.Certificates, p.Storage = minusFold(p.Certificates, e.Certificates), minusFold(p.Storage, e.Storage)

		if len(p.Keys)+len(p.Secrets)+len(p.Certificates)+len(p.Storage) == 0 {
			return append(list[:idx], list[idx+1:]...)
		}
	}

	return list
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
