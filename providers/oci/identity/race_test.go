package identity

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// The stores hand back pointers, so an update mutates the very record a
// concurrent reader is walking. These tests fail under -race if the Mock's
// mutex is dropped from the update or read paths; a torn read of p.parsed
// during Evaluate misevaluates authorization.

const raceGoroutines = 16

const readBucketsInDev = "Allow group Admins to read buckets in compartment dev"

func TestPolicyUpdateConcurrentWithEvaluate(t *testing.T) {
	t.Parallel()

	m := newMock(t)
	ctx := t.Context()
	dev := newCompartment(t, m, tenancy, devName)

	pol, err := m.CreateStatementPolicy(ctx, &PolicySpec{
		CompartmentID: tenancy, Name: "admins", Statements: []string{manageAllInDev},
	})
	require.NoError(t, err)

	var wg sync.WaitGroup

	for i := range raceGoroutines {
		wg.Add(3)

		go func() {
			defer wg.Done()

			text := manageAllInDev
			if i%2 == 1 {
				text = readBucketsInDev
			}

			if _, err := m.UpdateStatementPolicy(ctx, pol.ID, PolicyUpdate{
				Statements: []string{text},
			}); err != nil {
				t.Errorf("UpdateStatementPolicy: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			if _, err := m.Evaluate(ctx, &AccessRequest{
				Groups:        []string{adminName},
				Verb:          verbRead,
				ResourceType:  "buckets",
				CompartmentID: dev,
			}); err != nil {
				t.Errorf("Evaluate: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			if _, err := m.ListPolicyVersions(ctx, pol.ID); err != nil {
				t.Errorf("ListPolicyVersions: %v", err)
			}
		}()
	}

	wg.Wait()
}

func TestPolicyVersionWritesConcurrentWithReads(t *testing.T) {
	t.Parallel()

	m := newMock(t)
	ctx := t.Context()

	pol, err := m.CreateStatementPolicy(ctx, &PolicySpec{
		CompartmentID: tenancy, Name: "admins", Statements: []string{manageAllInDev},
	})
	require.NoError(t, err)

	var wg sync.WaitGroup

	for range raceGoroutines {
		wg.Add(2)

		go func() {
			defer wg.Done()

			if _, err := m.CreatePolicyVersion(ctx, driver.PolicyVersionConfig{
				PolicyARN: pol.ID, PolicyDocument: readBucketsInDev, SetAsDefault: true,
			}); err != nil {
				t.Errorf("CreatePolicyVersion: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			if _, err := m.GetPolicy(ctx, pol.ID); err != nil {
				t.Errorf("GetPolicy: %v", err)
			}
		}()
	}

	wg.Wait()
}

func TestPrincipalAndCompartmentUpdatesConcurrentWithReads(t *testing.T) {
	t.Parallel()

	m := newMock(t)
	ctx := t.Context()
	dev := newCompartment(t, m, tenancy, devName)

	user, err := m.CreateOCIUser(ctx, PrincipalSpec{CompartmentID: dev, Name: "alice"})
	require.NoError(t, err)

	var wg sync.WaitGroup

	for i := range raceGoroutines {
		wg.Add(4)

		go func() {
			defer wg.Done()

			if _, err := m.UpdateOCIUser(ctx, user.ID, Update{
				Description:  "rev",
				FreeformTags: map[string]string{"env": devName},
			}); err != nil {
				t.Errorf("UpdateOCIUser: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			if _, err := m.ListOCIUsers(ctx, dev); err != nil {
				t.Errorf("ListOCIUsers: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			name := devName
			if i%2 == 1 {
				name = "development"
			}

			if _, err := m.UpdateCompartment(ctx, dev, Update{Name: name}); err != nil {
				t.Errorf("UpdateCompartment: %v", err)
			}
		}()

		go func() {
			defer wg.Done()

			if _, err := m.ListCompartments(ctx, tenancy, true); err != nil {
				t.Errorf("ListCompartments: %v", err)
			}
		}()
	}

	wg.Wait()
}
