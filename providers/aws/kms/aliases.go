package kms

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/kms/driver"
)

const aliasPrefix = "alias/"

func validateAliasName(name string) error {
	if !strings.HasPrefix(name, aliasPrefix) {
		return errors.Newf(errors.InvalidArgument, "alias name must start with %q", aliasPrefix)
	}

	if strings.HasPrefix(name, aliasPrefix+"aws/") {
		return errors.Newf(errors.InvalidArgument, "the alias/aws/ prefix is reserved")
	}

	if name == aliasPrefix {
		return errors.New(errors.InvalidArgument, "alias name must not be empty")
	}

	return nil
}

// CreateAlias points a new alias at a key.
func (m *Mock) CreateAlias(_ context.Context, aliasName, targetKeyID string) error {
	if err := validateAliasName(aliasName); err != nil {
		return err
	}

	id, err := m.resolveKeyID(targetKeyID)
	if err != nil {
		return err
	}

	if !m.keys.Has(id) {
		return errors.Newf(errors.NotFound, "key %q not found", targetKeyID)
	}

	if m.aliases.Has(aliasName) {
		return errors.Newf(errors.AlreadyExists, "alias %q already exists", aliasName)
	}

	now := m.now()
	m.aliases.Set(aliasName, &aliasData{
		name:        aliasName,
		arn:         m.aliasARN(aliasName),
		targetKeyID: id,
		created:     now,
		updated:     now,
	})

	return nil
}

// UpdateAlias repoints an existing alias at a different key.
func (m *Mock) UpdateAlias(_ context.Context, aliasName, targetKeyID string) error {
	id, err := m.resolveKeyID(targetKeyID)
	if err != nil {
		return err
	}

	if !m.keys.Has(id) {
		return errors.Newf(errors.NotFound, "key %q not found", targetKeyID)
	}

	a, ok := m.aliases.Get(aliasName)
	if !ok {
		return errors.Newf(errors.NotFound, "alias %q not found", aliasName)
	}

	a.targetKeyID = id
	a.updated = m.now()

	return nil
}

// DeleteAlias removes an alias.
func (m *Mock) DeleteAlias(_ context.Context, aliasName string) error {
	if !m.aliases.Delete(aliasName) {
		return errors.Newf(errors.NotFound, "alias %q not found", aliasName)
	}

	return nil
}

// ListAliases lists all aliases, or only those pointing at keyID when set.
func (m *Mock) ListAliases(_ context.Context, keyID string) ([]driver.Alias, error) {
	var filter string

	if keyID != "" {
		id, err := m.resolveKeyID(keyID)
		if err != nil {
			return nil, err
		}

		filter = id
	}

	all := m.aliases.All()
	out := make([]driver.Alias, 0, len(all))

	for _, a := range all {
		if filter != "" && a.targetKeyID != filter {
			continue
		}

		out = append(out, driver.Alias{
			Name:         a.name,
			ARN:          a.arn,
			TargetKeyID:  a.targetKeyID,
			CreationDate: a.created,
			UpdatedDate:  a.updated,
		})
	}

	return out, nil
}

// TagResource adds or overwrites tags on a key.
func (m *Mock) TagResource(_ context.Context, keyID string, tags map[string]string) error {
	return m.mutateKey(keyID, func(kd *keyData) error {
		for k, v := range tags {
			kd.tags[k] = v
		}

		return nil
	})
}

// UntagResource removes tags from a key.
func (m *Mock) UntagResource(_ context.Context, keyID string, tagKeys []string) error {
	return m.mutateKey(keyID, func(kd *keyData) error {
		for _, k := range tagKeys {
			delete(kd.tags, k)
		}

		return nil
	})
}

// ListResourceTags returns a copy of a key's tags.
func (m *Mock) ListResourceTags(_ context.Context, keyID string) (map[string]string, error) {
	kd, err := m.getKey(keyID)
	if err != nil {
		return nil, err
	}

	kd.mu.RLock()
	defer kd.mu.RUnlock()

	return copyTags(kd.tags), nil
}
