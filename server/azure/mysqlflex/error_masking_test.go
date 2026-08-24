package mysqlflex_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	cerrors "github.com/stackshy/cloudemu/v2/errors"
	azmysql "github.com/stackshy/cloudemu/v2/providers/azure/mysqlflex"
	"github.com/stackshy/cloudemu/v2/server/azure/mysqlflex"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// createFailingDB wraps a real provider Mock but makes CreateInstance fail with a
// non-AlreadyExists error, and records whether the PUT handler falls through to
// ModifyInstance.
type createFailingDB struct {
	*azmysql.Mock
	createErr    error
	modifyCalled bool
}

//nolint:gocritic // signature matches the rdsdriver.RelationalDB interface.
func (d *createFailingDB) CreateInstance(_ context.Context, _ rdsdriver.InstanceConfig) (*rdsdriver.Instance, error) {
	return nil, d.createErr
}

func (d *createFailingDB) ModifyInstance(
	ctx context.Context, id string, in rdsdriver.ModifyInstanceInput,
) (*rdsdriver.Instance, error) {
	d.modifyCalled = true

	return d.Mock.ModifyInstance(ctx, id, in)
}

func newFailingDB(createErr error) *createFailingDB {
	opts := config.NewOptions(
		config.WithClock(config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))),
		config.WithRegion("eastus"),
	)

	return &createFailingDB{Mock: azmysql.New(opts), createErr: createErr}
}

// A genuine (non-AlreadyExists) CreateInstance failure on PUT must surface its
// own error, not be masked by an idempotent-PUT ModifyInstance fallback that
// returns a fabricated 404. Real Azure ARM returns 400 for a validation failure.
func TestPutServerCreateErrorNotMasked(t *testing.T) {
	db := newFailingDB(cerrors.New(cerrors.InvalidArgument, "sku is not available in this region"))
	h := mysqlflex.New(db)

	const path = "/subscriptions/sub-1/resourceGroups/rg-1/providers/" +
		"Microsoft.DBforMySQL/flexibleServers/srv1"

	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{"location":"eastus","properties":{}}`))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (original InvalidArgument preserved); body: %s", rec.Code, rec.Body.String())
	}

	if db.modifyCalled {
		t.Error("ModifyInstance was called for a non-AlreadyExists create error; the original error was masked")
	}
}

// The idempotent-PUT upsert path is unchanged: an AlreadyExists create still
// falls through to ModifyInstance and returns 200.
func TestPutServerAlreadyExistsUpserts(t *testing.T) {
	db := newFailingDB(cerrors.New(cerrors.AlreadyExists, "server already exists"))
	if _, err := db.Mock.CreateInstance(context.Background(), rdsdriver.InstanceConfig{ID: "srv1"}); err != nil {
		t.Fatalf("seed CreateInstance: %v", err)
	}

	h := mysqlflex.New(db)

	const path = "/subscriptions/sub-1/resourceGroups/rg-1/providers/" +
		"Microsoft.DBforMySQL/flexibleServers/srv1"

	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{"location":"eastus","properties":{}}`))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	if !db.modifyCalled {
		t.Error("ModifyInstance was not called for an AlreadyExists create; upsert path broken")
	}
}
