package cognito

import (
	"context"
	"errors"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

func assertInvalidParameter(t *testing.T, err error, wantMsg string) {
	t.Helper()

	var apiErr *driver.APIError
	if !errors.As(err, &apiErr) || apiErr.Exception != driver.ExInvalidParameter {
		t.Fatalf("expected InvalidParameterException, got %v", err)
	}

	if wantMsg != "" && cerrors.Message(err) != wantMsg {
		t.Fatalf("message = %q, want %q", cerrors.Message(err), wantMsg)
	}
}

func TestDeleteUserPoolBlockedByDomain(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()
	pool := mustCreatePool(t, m, "domain-pool")

	requireNoError(t, m.CreateUserPoolDomain(ctx,
		driver.CreateUserPoolDomainInput{Domain: "d.example", UserPoolID: pool.ID}), "CreateUserPoolDomain")

	assertInvalidParameter(t, m.DeleteUserPool(ctx, pool.ID),
		"User pool cannot be deleted. It has a domain configured that should be deleted first.")

	// The pool and its domain are both untouched by the refused delete.
	if _, err := m.DescribeUserPool(ctx, pool.ID); err != nil {
		t.Fatalf("pool gone after refused delete: %v", err)
	}

	dom, err := m.DescribeUserPoolDomain(ctx, "d.example")
	requireNoError(t, err, "DescribeUserPoolDomain")

	if dom.UserPoolID != pool.ID {
		t.Fatalf("domain changed after refused delete: %+v", dom)
	}

	requireNoError(t, m.DeleteUserPoolDomain(ctx, "d.example", pool.ID), "DeleteUserPoolDomain")
	requireNoError(t, m.DeleteUserPool(ctx, pool.ID), "DeleteUserPool after domain removed")
}

func TestDeleteUserPoolBlockedByDeletionProtection(t *testing.T) {
	m := newMock(t)
	ctx := context.Background()

	pool, err := m.CreateUserPool(ctx, driver.CreateUserPoolInput{
		Name:               "protected",
		DeletionProtection: driver.DeletionProtectionActive,
	})
	requireNoError(t, err, "CreateUserPool")

	assertInvalidParameter(t, m.DeleteUserPool(ctx, pool.ID),
		"The user pool cannot be deleted because deletion protection is activated. Deletion protection must be inactivated first.")

	requireNoError(t, m.UpdateUserPool(ctx, driver.UpdateUserPoolInput{
		ID:                 pool.ID,
		DeletionProtection: driver.DeletionProtectionInactive,
	}), "UpdateUserPool")

	requireNoError(t, m.DeleteUserPool(ctx, pool.ID), "DeleteUserPool after protection off")
	assertNotFound(t, m.DeleteUserPool(ctx, pool.ID))
}

func TestDeletePoolCascadesClients(t *testing.T) {
	m := newMock(t)
	pool := mustCreatePool(t, m, "cascade-pool")
	ctx := context.Background()

	client, err := m.CreateUserPoolClient(ctx, driver.CreateUserPoolClientInput{UserPoolID: pool.ID, ClientName: "c"})
	requireNoError(t, err, "CreateUserPoolClient")

	requireNoError(t, m.DeleteUserPool(ctx, pool.ID), "DeleteUserPool")

	if _, err := m.DescribeUserPoolClient(ctx, pool.ID, client.ClientID); !cerrors.IsNotFound(err) {
		t.Fatal("client not removed on pool delete")
	}
}
