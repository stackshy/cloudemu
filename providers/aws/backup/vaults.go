package backup

import (
	"context"
	"regexp"
	"time"

	"github.com/stackshy/cloudemu/v2/services/backup/driver"
)

// vaultNamePattern is the AWS Backup vault-name constraint.
var vaultNamePattern = regexp.MustCompile(`^[a-zA-Z0-9\-\_]{2,50}$`)

// minChangeableForDays is the AWS-enforced cooling-off floor (72 hours) before a
// COMPLIANCE-mode Vault Lock takes effect.
const minChangeableForDays = 3

const hoursPerDay = 24

// CreateBackupVault creates a logical container for backups with stable computed
// fields (arn, creationDate) minted once and stored.
func (m *Mock) CreateBackupVault(_ context.Context, in *driver.CreateVaultInput) (*driver.Vault, error) {
	if !vaultNamePattern.MatchString(in.Name) {
		return nil, invalidParam("invalid backup vault name: %q", in.Name)
	}

	if m.vaults.Has(in.Name) {
		return nil, alreadyExists("backup vault %s already exists", in.Name)
	}

	v := driver.Vault{
		Name:             in.Name,
		Arn:              m.vaultARN(in.Name),
		CreatorRequestID: in.CreatorRequestID,
		EncryptionKeyArn: in.EncryptionKeyArn,
		CreationDate:     m.now(),
		Tags:             copyTags(in.Tags),
	}

	m.vaults.Set(in.Name, v)

	out := copyVault(&v)

	return &out, nil
}

// DescribeBackupVault returns a copy of the named vault.
func (m *Mock) DescribeBackupVault(_ context.Context, name string) (*driver.Vault, error) {
	v, ok := m.vaults.Get(name)
	if !ok {
		return nil, notFound("backup vault %s not found", name)
	}

	out := copyVault(&v)

	return &out, nil
}

// DeleteBackupVault removes a vault. A vault whose Vault Lock has become
// immutable cannot be deleted.
func (m *Mock) DeleteBackupVault(_ context.Context, name string) error {
	v, ok := m.vaults.Get(name)
	if !ok {
		return notFound("backup vault %s not found", name)
	}

	if m.lockImmutable(&v) {
		return invalidRequest("backup vault %s is locked and cannot be deleted", name)
	}

	m.vaults.Delete(name)

	return nil
}

// ListBackupVaults returns a deterministic page of vaults ordered by name.
func (m *Mock) ListBackupVaults(_ context.Context, page driver.Page) ([]*driver.Vault, string, error) {
	stored := m.vaults.SortedValues()
	start, end, next := paginate(len(stored), page)
	out := make([]*driver.Vault, 0, end-start)

	for i := start; i < end; i++ {
		v := copyVault(&stored[i])
		out = append(out, &v)
	}

	return out, next, nil
}

// lockImmutable reports whether a vault's COMPLIANCE-mode Vault Lock has passed
// its LockDate and can no longer be changed or removed.
func (m *Mock) lockImmutable(v *driver.Vault) bool {
	return v.Locked && v.LockDate != nil && !m.now().Before(*v.LockDate)
}

// PutBackupVaultAccessPolicy attaches a resource-based access policy to a vault.
func (m *Mock) PutBackupVaultAccessPolicy(_ context.Context, name, policy string) error {
	v, ok := m.vaults.Get(name)
	if !ok {
		return notFound("backup vault %s not found", name)
	}

	v.AccessPolicy = policy
	m.vaults.Set(name, v)

	return nil
}

// GetBackupVaultAccessPolicy returns the vault and its access policy. A vault
// with no policy set reports ResourceNotFoundException, matching the service.
func (m *Mock) GetBackupVaultAccessPolicy(_ context.Context, name string) (*driver.Vault, string, error) {
	v, ok := m.vaults.Get(name)
	if !ok {
		return nil, "", notFound("backup vault %s not found", name)
	}

	if v.AccessPolicy == "" {
		return nil, "", notFound("no access policy for backup vault %s", name)
	}

	out := copyVault(&v)

	return &out, v.AccessPolicy, nil
}

// DeleteBackupVaultAccessPolicy removes a vault's access policy.
func (m *Mock) DeleteBackupVaultAccessPolicy(_ context.Context, name string) error {
	v, ok := m.vaults.Get(name)
	if !ok {
		return notFound("backup vault %s not found", name)
	}

	v.AccessPolicy = ""
	m.vaults.Set(name, v)

	return nil
}

// PutBackupVaultNotifications sets the SNS topic and events for a vault.
func (m *Mock) PutBackupVaultNotifications(_ context.Context, in *driver.PutVaultNotificationsInput) error {
	v, ok := m.vaults.Get(in.Name)
	if !ok {
		return notFound("backup vault %s not found", in.Name)
	}

	if in.SNSTopicArn == "" {
		return missingParam("SNSTopicArn is required")
	}

	v.SNSTopicArn = in.SNSTopicArn
	v.BackupVaultEvents = copyStrings(in.BackupVaultEvents)
	m.vaults.Set(in.Name, v)

	return nil
}

// GetBackupVaultNotifications returns the vault and its notification config. A
// vault with none set reports ResourceNotFoundException, matching the service.
func (m *Mock) GetBackupVaultNotifications(_ context.Context, name string) (*driver.Vault, driver.Notifications, error) {
	v, ok := m.vaults.Get(name)
	if !ok {
		return nil, driver.Notifications{}, notFound("backup vault %s not found", name)
	}

	if v.SNSTopicArn == "" {
		return nil, driver.Notifications{}, notFound("no notifications for backup vault %s", name)
	}

	out := copyVault(&v)
	n := driver.Notifications{SNSTopicArn: v.SNSTopicArn, BackupVaultEvents: copyStrings(v.BackupVaultEvents)}

	return &out, n, nil
}

// DeleteBackupVaultNotifications clears a vault's notification config.
func (m *Mock) DeleteBackupVaultNotifications(_ context.Context, name string) error {
	v, ok := m.vaults.Get(name)
	if !ok {
		return notFound("backup vault %s not found", name)
	}

	v.SNSTopicArn = ""
	v.BackupVaultEvents = nil
	m.vaults.Set(name, v)

	return nil
}

// PutBackupVaultLockConfiguration applies (or updates) Vault Lock. A ChangeableForDays
// value selects COMPLIANCE mode and fixes the LockDate that many days out; its
// absence selects GOVERNANCE mode. Once a COMPLIANCE lock's LockDate has passed
// the configuration is immutable.
func (m *Mock) PutBackupVaultLockConfiguration(_ context.Context, in *driver.PutVaultLockInput) error {
	v, ok := m.vaults.Get(in.Name)
	if !ok {
		return notFound("backup vault %s not found", in.Name)
	}

	if m.lockImmutable(&v) {
		return invalidRequest("backup vault %s Vault Lock is immutable and cannot be changed", in.Name)
	}

	if in.ChangeableForDays != nil && *in.ChangeableForDays < minChangeableForDays {
		return invalidParam("ChangeableForDays must be 3 or greater")
	}

	v.Locked = true
	v.MinRetentionDays = copyInt64(in.MinRetentionDays)
	v.MaxRetentionDays = copyInt64(in.MaxRetentionDays)

	if in.ChangeableForDays != nil {
		lockDate := m.now().Add(time.Duration(*in.ChangeableForDays) * hoursPerDay * time.Hour)
		v.LockDate = &lockDate
	} else {
		v.LockDate = nil
	}

	m.vaults.Set(in.Name, v)

	return nil
}

// DeleteBackupVaultLockConfiguration removes Vault Lock. An immutable
// COMPLIANCE-mode lock cannot be removed.
func (m *Mock) DeleteBackupVaultLockConfiguration(_ context.Context, name string) error {
	v, ok := m.vaults.Get(name)
	if !ok {
		return notFound("backup vault %s not found", name)
	}

	if m.lockImmutable(&v) {
		return invalidRequest("backup vault %s Vault Lock is immutable and cannot be removed", name)
	}

	v.Locked = false
	v.MinRetentionDays = nil
	v.MaxRetentionDays = nil
	v.LockDate = nil
	m.vaults.Set(name, v)

	return nil
}
