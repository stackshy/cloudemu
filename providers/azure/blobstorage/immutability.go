package blobstorage

import (
	"context"
	"net/http"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/storage/driver"
)

// Compile-time check that Mock satisfies the optional AzureImmutableBlob
// capability the blob wire handler reaches by type assertion.
var _ driver.AzureImmutableBlob = (*Mock)(nil)

// immutabilityBlock reports the WORM error blocking a delete or overwrite of
// obj, or nil when the blob is not currently protected. A legal hold protects
// the blob unconditionally; a time-based policy protects it until its
// retain-until instant elapses. The caller must hold obj.mu.
func immutabilityBlock(obj *blobObject, now time.Time) error {
	if obj.legalHold {
		return &driver.BlobOpError{
			Status:  http.StatusConflict,
			Code:    "BlobImmutableDueToLegalHold",
			Message: "This operation is not permitted as the blob is immutable due to one or more legal holds.",
		}
	}

	if obj.immutabilityMode != "" && now.Before(obj.immutabilityExpiry) {
		return &driver.BlobOpError{
			Status:  http.StatusConflict,
			Code:    "BlobImmutableDueToPolicy",
			Message: "This operation is not permitted as the blob is immutable due to a policy.",
		}
	}

	return nil
}

// enforceImmutable returns the WORM error blocking a delete/overwrite of the
// named blob, or nil when it is absent or not currently protected. It takes the
// blob's own lock for the read so it never races an in-flight policy/legal-hold
// change on the same blob.
func (m *Mock) enforceImmutable(ctr *containerMeta, key string) error {
	obj, ok := ctr.objects.Get(key)
	if !ok {
		return nil
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	return immutabilityBlock(obj, m.opts.Clock.Now().UTC())
}

// SetBlobImmutabilityPolicy implements driver.AzureImmutableBlob, setting or
// updating the blob's time-based retention policy. The retain-until date must be
// in the future. A Locked policy may only be extended and can never revert to
// Unlocked; an Unlocked policy may be raised, lowered, or promoted to Locked.
func (m *Mock) SetBlobImmutabilityPolicy(
	_ context.Context, container, blob string, policy driver.BlobImmutabilityPolicy,
) (driver.BlobImmutabilityPolicy, error) {
	mode := policy.Mode
	if mode == "" {
		mode = driver.BlobImmutabilityUnlocked
	}

	if mode != driver.BlobImmutabilityUnlocked && mode != driver.BlobImmutabilityLocked {
		return driver.BlobImmutabilityPolicy{}, cerrors.Newf(cerrors.InvalidArgument,
			"invalid immutability policy mode %q: must be Unlocked or Locked", policy.Mode)
	}

	obj, err := m.getBlobObject(container, blob)
	if err != nil {
		return driver.BlobImmutabilityPolicy{}, err
	}

	if !policy.ExpiryTime.After(m.opts.Clock.Now().UTC()) {
		return driver.BlobImmutabilityPolicy{}, cerrors.New(cerrors.InvalidArgument,
			"immutability policy retain-until date must be in the future")
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	if obj.immutabilityMode == driver.BlobImmutabilityLocked {
		if mode != driver.BlobImmutabilityLocked {
			return driver.BlobImmutabilityPolicy{}, cerrors.New(cerrors.FailedPrecondition,
				"a locked immutability policy cannot be changed to unlocked")
		}

		if policy.ExpiryTime.Before(obj.immutabilityExpiry) {
			return driver.BlobImmutabilityPolicy{}, cerrors.New(cerrors.FailedPrecondition,
				"a locked immutability policy retain-until date can only be extended")
		}
	}

	obj.immutabilityMode = mode
	obj.immutabilityExpiry = policy.ExpiryTime

	return driver.BlobImmutabilityPolicy{ExpiryTime: obj.immutabilityExpiry, Mode: obj.immutabilityMode}, nil
}

// DeleteBlobImmutabilityPolicy implements driver.AzureImmutableBlob, removing an
// Unlocked immutability policy. A Locked policy can never be deleted. Removing a
// policy that was never set is a no-op success.
func (m *Mock) DeleteBlobImmutabilityPolicy(_ context.Context, container, blob string) error {
	obj, err := m.getBlobObject(container, blob)
	if err != nil {
		return err
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	if obj.immutabilityMode == driver.BlobImmutabilityLocked {
		return &driver.BlobOpError{
			Status:  http.StatusConflict,
			Code:    "BlobImmutableDueToPolicy",
			Message: "This operation is not permitted as the blob is immutable due to a policy.",
		}
	}

	obj.immutabilityMode = ""
	obj.immutabilityExpiry = time.Time{}

	return nil
}

// SetBlobLegalHold implements driver.AzureImmutableBlob, setting or clearing the
// blob's legal hold.
func (m *Mock) SetBlobLegalHold(_ context.Context, container, blob string, hold bool) error {
	obj, err := m.getBlobObject(container, blob)
	if err != nil {
		return err
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	obj.legalHold = hold

	return nil
}

// BlobImmutability implements driver.AzureImmutableBlob, reporting the blob's
// current immutability policy and legal hold for Get Blob Properties.
func (m *Mock) BlobImmutability(
	_ context.Context, container, blob string,
) (driver.BlobImmutabilityPolicy, bool, error) {
	obj, err := m.getBlobObject(container, blob)
	if err != nil {
		return driver.BlobImmutabilityPolicy{}, false, err
	}

	obj.mu.Lock()
	defer obj.mu.Unlock()

	return driver.BlobImmutabilityPolicy{ExpiryTime: obj.immutabilityExpiry, Mode: obj.immutabilityMode}, obj.legalHold, nil
}
