package backupdr

import (
	"maps"

	bdrdriver "github.com/stackshy/cloudemu/v2/services/backupdr/driver"
)

// cloneVault returns a deep copy of v so a stored vault is never aliased by a
// value handed back to a caller (which the wire layer would otherwise be free to
// mutate). The label/annotation maps and the encryption config are copied.
func cloneVault(v *bdrdriver.BackupVault) bdrdriver.BackupVault {
	out := *v
	out.Labels = cloneStrMap(v.Labels)
	out.Annotations = cloneStrMap(v.Annotations)
	out.EncryptionConfig = cloneEncryption(v.EncryptionConfig)

	return out
}

// cloneStrMap copies a string map; an empty map clones to nil.
func cloneStrMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	return maps.Clone(in)
}

// cloneEncryption copies an optional encryption config.
func cloneEncryption(in *bdrdriver.EncryptionConfig) *bdrdriver.EncryptionConfig {
	if in == nil {
		return nil
	}

	out := *in

	return &out
}
