package efs

import (
	"time"

	"github.com/stackshy/cloudemu/v2/services/efs/driver"
)

// Settle windows for EFS's asynchronous lifecycle transitions. Real EFS returns
// from CreateFileSystem / CreateMountTarget while the resource is still
// "creating" and from DeleteFileSystem / DeleteMountTarget while it is still
// "deleting". They only apply when config.Options.AsyncSettle is set
// (SettleDuration returns 0 otherwise), so the default stays synchronous:
// resources are "available" immediately and deletes are instant. They are short
// so an SDK waiter succeeds on its first poll while an immediate Describe still
// observes the transient state.
const (
	fileSystemCreateSettle  = 2 * time.Second
	fileSystemDeleteSettle  = 1 * time.Second
	mountTargetCreateSettle = 2 * time.Second
	mountTargetDeleteSettle = 1 * time.Second
)

func (m *Mock) now() time.Time { return m.opts.Clock.Now() }

// settleDeadline returns now+d when async settling is enabled, else the zero
// time (no window).
func (m *Mock) settleDeadline(d time.Duration) time.Time {
	if d = m.opts.SettleDuration(d); d <= 0 {
		return time.Time{}
	}

	return m.now().Add(d)
}

// fsStateLocked is the lifecycle state a file system reports now: "deleting"
// once DeleteFileSystem has accepted it, "creating" until its create window
// elapses, otherwise its stored (available) state. Callers hold fd.mu.
func (m *Mock) fsStateLocked(fd *fsData) string {
	switch {
	case !fd.deleteAt.IsZero():
		return driver.StateDeleting
	case m.now().Before(fd.readyAt):
		return driver.StateCreating
	default:
		return fd.fs.LifeCycleState
	}
}

// fsViewLocked returns the file system as callers see it, with the lifecycle
// state overlaid. Callers hold fd.mu.
func (m *Mock) fsViewLocked(fd *fsData) driver.FileSystem {
	out := fd.fs
	out.LifeCycleState = m.fsStateLocked(fd)

	return out
}

// requireAvailableLocked rejects an operation on a file system that is not
// "available" with IncorrectFileSystemLifeCycleState, as real EFS does for
// CreateMountTarget, CreateAccessPoint, UpdateFileSystem, the policy/backup/
// lifecycle/replication writes and UpdateFileSystemProtection. Callers hold fd.mu.
func (m *Mock) requireAvailableLocked(fd *fsData) error {
	if st := m.fsStateLocked(fd); st != driver.StateAvailable {
		return incorrectFSState(fd.fs.FileSystemID, st)
	}

	return nil
}

// mtStateLocked is the lifecycle state a mount target reports now. Callers hold
// fd.mu.
func (m *Mock) mtStateLocked(fd *fsData, mt *driver.MountTarget) string {
	if _, ok := fd.mtDeleteAt[mt.MountTargetID]; ok {
		return driver.StateDeleting
	}

	if ready, ok := fd.mtReadyAt[mt.MountTargetID]; ok && m.now().Before(ready) {
		return driver.StateCreating
	}

	return mt.LifeCycleState
}

// mtViewLocked returns a copy of a mount target with its lifecycle state
// overlaid. Callers hold fd.mu.
func (m *Mock) mtViewLocked(fd *fsData, mt *driver.MountTarget) driver.MountTarget {
	out := *mt
	out.LifeCycleState = m.mtStateLocked(fd, mt)

	return out
}

// getFS resolves a file system by id, first finishing any deletion whose
// window has elapsed: a file system past its "deleting" window is gone
// (FileSystemNotFound), and mount targets past theirs are removed.
func (m *Mock) getFS(id string) (*fsData, bool) {
	fd, ok := m.fileSystems.Get(id)
	if !ok {
		return nil, false
	}

	if m.reapFS(id, fd) {
		return nil, false
	}

	m.reapMountTargets(fd)

	return fd, true
}

// reapFS removes a file system whose deletion window has elapsed, releasing its
// creation token, and reports whether it did.
func (m *Mock) reapFS(id string, fd *fsData) bool {
	fd.mu.RLock()
	gone := !fd.deleteAt.IsZero() && !m.now().Before(fd.deleteAt)
	token := fd.fs.CreationToken
	fd.mu.RUnlock()

	if !gone {
		return false
	}

	m.fileSystems.Delete(id)

	if owner, ok := m.tokenIndex.Get(token); ok && owner == id {
		m.tokenIndex.Delete(token)
	}

	return true
}

// reapMountTargets removes mount targets whose deletion window has elapsed and
// drops create windows that have elapsed.
func (m *Mock) reapMountTargets(fd *fsData) {
	fd.mu.Lock()
	defer fd.mu.Unlock()

	now := m.now()

	for id, ready := range fd.mtReadyAt {
		if !now.Before(ready) {
			delete(fd.mtReadyAt, id)
		}
	}

	removed := false

	for id, at := range fd.mtDeleteAt {
		if now.Before(at) {
			continue
		}

		delete(fd.mtDeleteAt, id)
		delete(fd.mtReadyAt, id)
		delete(fd.mountTgts, id)
		m.mtIndex.Delete(id)

		removed = true
	}

	if removed {
		//nolint:gosec // mount-target count is tiny, never near int32 max
		fd.fs.NumberOfMountTargets = int32(len(fd.mountTgts))
	}
}

// removeMountTargetLocked deletes a mount target immediately. Callers hold fd.mu
// for writing.
func (m *Mock) removeMountTargetLocked(fd *fsData, mountTargetID string) {
	delete(fd.mountTgts, mountTargetID)
	delete(fd.mtReadyAt, mountTargetID)
	delete(fd.mtDeleteAt, mountTargetID)
	//nolint:gosec // mount-target count is tiny, never near int32 max
	fd.fs.NumberOfMountTargets = int32(len(fd.mountTgts))

	m.mtIndex.Delete(mountTargetID)
}
