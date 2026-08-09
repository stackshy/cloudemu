package sesv2

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// PutSuppressedDestination adds (or updates) an address on the suppression list.
func (m *Mock) PutSuppressedDestination(_ context.Context, in driver.PutSuppressedInput) error {
	if in.EmailAddress == "" {
		return cerrors.New(cerrors.InvalidArgument, "EmailAddress is required")
	}

	reason := in.Reason
	if reason == "" {
		reason = driver.SuppressionReasonBounce
	}

	m.suppressed.Set(in.EmailAddress, driver.SuppressedDestination{
		EmailAddress:   in.EmailAddress,
		Reason:         reason,
		LastUpdateTime: m.now(),
	})

	return nil
}

// GetSuppressedDestination returns a suppressed address.
func (m *Mock) GetSuppressedDestination(_ context.Context, addr string) (*driver.SuppressedDestination, error) {
	s, ok := m.suppressed.Get(addr)
	if !ok {
		return nil, errSuppressedNotFound(addr)
	}

	return &s, nil
}

// DeleteSuppressedDestination removes an address from the suppression list.
func (m *Mock) DeleteSuppressedDestination(_ context.Context, addr string) error {
	if !m.suppressed.Delete(addr) {
		return errSuppressedNotFound(addr)
	}

	return nil
}

// ListSuppressedDestinations returns all suppressed addresses ordered.
func (m *Mock) ListSuppressedDestinations(_ context.Context) ([]driver.SuppressedDestination, error) {
	return m.suppressed.SortedValues(), nil
}

func errSuppressedNotFound(addr string) error {
	return cerrors.Newf(cerrors.NotFound, "suppressed destination %q does not exist", addr)
}
