package resourcediscovery

import (
	"context"
	"errors"
	"testing"
)

type fakeRelational struct {
	dbs []DiscoveredDatabase
	err error
}

func (f fakeRelational) DiscoverDatabases(context.Context) ([]DiscoveredDatabase, error) {
	if f.err != nil {
		return nil, f.err
	}

	return f.dbs, nil
}

// Managed relational servers must surface in the inventory, carrying the
// per-cloud portable Type and their own region/ARN.
func TestWalkRelationalDBSurfacesServers(t *testing.T) {
	eng := New(ProviderAzure, "sub-1", "eastus", &Drivers{
		RelationalDB: fakeRelational{dbs: []DiscoveredDatabase{
			{Name: "sql1", Type: TypeSQLServer, ARN: "/subscriptions/sub-1/.../servers/sql1"},
			{Name: "my1", Type: TypeMySQLFlex, Region: "westus", ARN: "arn-my1"},
			{Name: "pg1", Type: TypePostgresFlex}, // region falls back to engine default
		}},
	})

	got, err := eng.walkRelationalDB(context.Background())
	if err != nil {
		t.Fatalf("walkRelationalDB: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("got %d resources, want 3", len(got))
	}

	byName := map[string]Resource{}
	for i := range got {
		if got[i].Service != ServiceDatabase {
			t.Errorf("%s: service = %q, want %q", got[i].ID, got[i].Service, ServiceDatabase)
		}

		byName[got[i].ID] = got[i]
	}

	if byName["sql1"].Type != TypeSQLServer {
		t.Errorf("sql1 type = %q, want %q", byName["sql1"].Type, TypeSQLServer)
	}

	if byName["my1"].Region != "westus" {
		t.Errorf("my1 region = %q, want westus", byName["my1"].Region)
	}

	if byName["pg1"].Region != "eastus" {
		t.Errorf("pg1 region = %q, want engine default eastus", byName["pg1"].Region)
	}
}

func TestWalkRelationalDBPropagatesErrors(t *testing.T) {
	eng := New(ProviderGCP, "proj", "us-east1", &Drivers{
		RelationalDB: fakeRelational{err: errors.New("list databases failed")},
	})

	if _, err := eng.walkRelationalDB(context.Background()); err == nil {
		t.Error("a failing database listing should surface, not be swallowed")
	}
}
