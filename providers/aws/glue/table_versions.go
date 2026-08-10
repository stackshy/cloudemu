package glue

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// GetTableVersion returns a specific stored version of a table.
func (m *Mock) GetTableVersion(
	_ context.Context, catalogID, dbName, tblName, versionID string,
) (*driver.TableVersion, error) {
	td, _, err := m.getTableData(catalogID, dbName, tblName)
	if err != nil {
		return nil, err
	}

	td.mu.RLock()
	defer td.mu.RUnlock()

	// An empty versionID means "the latest", matching real Glue.
	if versionID == "" {
		if len(td.versions) == 0 {
			return nil, entityNotFound("No version found for table %s", tblName)
		}

		last := td.versions[len(td.versions)-1]

		return &driver.TableVersion{Table: copyTable(last.Table), VersionID: last.VersionID}, nil
	}

	for i := range td.versions {
		if td.versions[i].VersionID == versionID {
			return &driver.TableVersion{
				Table: copyTable(td.versions[i].Table), VersionID: versionID,
			}, nil
		}
	}

	return nil, entityNotFound("Version not found: %s for table %s", versionID, tblName)
}

// GetTableVersions lists all stored versions of a table with pagination.
func (m *Mock) GetTableVersions(
	_ context.Context, catalogID, dbName, tblName string, page driver.TablePagination,
) ([]driver.TableVersion, string, error) {
	td, _, err := m.getTableData(catalogID, dbName, tblName)
	if err != nil {
		return nil, "", err
	}

	td.mu.RLock()
	all := make([]driver.TableVersion, 0, len(td.versions))

	for i := range td.versions {
		all = append(all, driver.TableVersion{
			Table: copyTable(td.versions[i].Table), VersionID: td.versions[i].VersionID,
		})
	}
	td.mu.RUnlock()

	return paginate(all, page)
}

// DeleteTableVersion removes a specific version of a table.
func (m *Mock) DeleteTableVersion(_ context.Context, catalogID, dbName, tblName, versionID string) error {
	td, _, err := m.getTableData(catalogID, dbName, tblName)
	if err != nil {
		return err
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	for i := range td.versions {
		if td.versions[i].VersionID == versionID {
			// Never leave a table with zero versions: a later "latest"
			// GetTableVersion would then have nothing to return. Real Glue
			// rejects deleting the only remaining version.
			if len(td.versions) == 1 {
				return entityNotFound("Cannot delete the only version %s for table %s", versionID, tblName)
			}

			td.versions = append(td.versions[:i], td.versions[i+1:]...)

			return nil
		}
	}

	return entityNotFound("Version not found: %s for table %s", versionID, tblName)
}

// BatchDeleteTableVersion deletes several versions, collecting per-version
// errors. Version IDs are validated (non-empty) before any delete.
func (m *Mock) BatchDeleteTableVersion(
	_ context.Context, catalogID, dbName, tblName string, ids []string,
) ([]driver.BatchError, error) {
	for _, id := range ids {
		if id == "" {
			return nil, invalidInput("version id must not be empty")
		}
	}

	var errs []driver.BatchError

	for _, id := range ids {
		if err := m.DeleteTableVersion(context.Background(), catalogID, dbName, tblName, id); err != nil {
			errs = append(errs, driver.BatchError{
				Values: []string{id}, ErrorCode: driver.ExEntityNotFound, ErrorMessage: err.Error(),
			})
		}
	}

	return errs, nil
}
