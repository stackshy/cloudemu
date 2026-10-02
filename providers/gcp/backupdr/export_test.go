package backupdr

// SetUsage seeds a vault's output-only backupCount and totalStoredBytes. The
// emulator has no data plane, so a test uses this to make a vault non-empty
// (deletable=false) and exercise the force-delete guard. It is a test seam
// only: it is compiled into this package's tests and is not production API.
func (m *Mock) SetUsage(project, location, id string, backupCount, totalStoredBytes int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := resourceName(project, location, id)

	v, ok := m.vaults.Get(key)
	if !ok {
		return notFoundErr(project, location, id)
	}

	v.BackupCount = backupCount
	v.TotalStoredBytes = totalStoredBytes
	m.vaults.Set(key, v)

	return nil
}
