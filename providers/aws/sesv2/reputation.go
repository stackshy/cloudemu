package sesv2

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// Reputation entities are materialized on first reference: the emulator has no
// real reputation signal, so an entity reports HEALTHY until a caller changes
// its managed status or policy.

func repKey(entityType, reference string) string {
	return entityType + "/" + reference
}

// GetReputationEntity returns (materializing if needed) a reputation entity.
func (m *Mock) GetReputationEntity(_ context.Context, entityType, reference string) (*driver.ReputationEntity, error) {
	if reference == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "ReputationEntityReference is required")
	}

	key := repKey(entityType, reference)

	e, ok := m.repEntities.Get(key)
	if !ok {
		e = &driver.ReputationEntity{
			Reference:             reference,
			EntityType:            entityType,
			CustomerManagedStatus: driver.ReputationStatusHealthy,
			AWSManagedStatus:      driver.ReputationStatusHealthy,
		}
		m.repEntities.Set(key, e)
	}

	out := *e

	return &out, nil
}

// ListReputationEntities returns all materialized reputation entities.
func (m *Mock) ListReputationEntities(_ context.Context) ([]driver.ReputationEntity, error) {
	all := m.repEntities.SortedValues()
	out := make([]driver.ReputationEntity, 0, len(all))

	for _, e := range all {
		out = append(out, *e)
	}

	return out, nil
}

// UpdateReputationEntityCustomerManagedStatus sets an entity's managed status.
func (m *Mock) UpdateReputationEntityCustomerManagedStatus(
	_ context.Context, entityType, reference, status string,
) error {
	if _, err := m.GetReputationEntity(context.Background(), entityType, reference); err != nil {
		return err
	}

	m.repEntities.Update(repKey(entityType, reference), func(e *driver.ReputationEntity) *driver.ReputationEntity {
		e.CustomerManagedStatus = status

		return e
	})

	return nil
}

// UpdateReputationEntityPolicy sets an entity's reputation management policy.
func (m *Mock) UpdateReputationEntityPolicy(_ context.Context, entityType, reference, policy string) error {
	if _, err := m.GetReputationEntity(context.Background(), entityType, reference); err != nil {
		return err
	}

	m.repEntities.Update(repKey(entityType, reference), func(e *driver.ReputationEntity) *driver.ReputationEntity {
		e.ReputationManagementPolicy = policy

		return e
	})

	return nil
}
