package dynamodb

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

// throughputUpdater is the optional provider capability UpdateTable needs to
// change a table's billing mode / provisioned throughput. Discovered by type
// assertion so it stays off the cross-cloud Database interface (only the AWS
// mock implements it).
type throughputUpdater interface {
	UpdateThroughput(ctx context.Context, table, billingMode string, rcu, wcu int64) error
}

// pitrController is the optional provider capability backing
// UpdateContinuousBackups / DescribeContinuousBackups.
type pitrController interface {
	SetPITR(ctx context.Context, table string, enabled bool) error
	GetPITR(ctx context.Context, table string) (bool, error)
}

// updateTable handles UpdateTable: billing/throughput changes, GSI create/delete
// updates and stream enable/disable. It returns the refreshed TableDescription,
// matching real DynamoDB. Without it the SDK sees UnknownOperationException and
// every throughput/GSI/stream mutation fails.
func (h *Handler) updateTable(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName             string        `json:"TableName"`
		BillingMode           string        `json:"BillingMode"`
		AttributeDefinitions  []attrDefJSON `json:"AttributeDefinitions"`
		ProvisionedThroughput *struct {
			ReadCapacityUnits  int64 `json:"ReadCapacityUnits"`
			WriteCapacityUnits int64 `json:"WriteCapacityUnits"`
		} `json:"ProvisionedThroughput"`
		GlobalSecondaryIndexUpdates []gsiUpdateJSON `json:"GlobalSecondaryIndexUpdates"`
		StreamSpecification         *struct {
			StreamEnabled  bool   `json:"StreamEnabled"`
			StreamViewType string `json:"StreamViewType"`
		} `json:"StreamSpecification"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if err := validateGSIUpdates(req.GlobalSecondaryIndexUpdates, req.AttributeDefinitions); err != nil {
		writeErr(w, err)
		return
	}

	if err := h.applyThroughput(r.Context(), req.TableName, req.BillingMode, req.ProvisionedThroughput); err != nil {
		writeErr(w, err)
		return
	}

	for i := range req.GlobalSecondaryIndexUpdates {
		if err := h.applyGSIUpdate(r.Context(), req.TableName,
			req.GlobalSecondaryIndexUpdates[i].Create,
			indexDeleteName(req.GlobalSecondaryIndexUpdates[i].Delete)); err != nil {
			writeErr(w, err)
			return
		}
	}

	if req.StreamSpecification != nil {
		cfg := dbdriver.StreamConfig{Enabled: req.StreamSpecification.StreamEnabled, ViewType: req.StreamSpecification.StreamViewType}
		if err := h.db.UpdateStreamConfig(r.Context(), req.TableName, cfg); err != nil {
			writeErr(w, err)
			return
		}
	}

	full, err := h.db.DescribeTable(r.Context(), req.TableName)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"TableDescription": tableDescription(full)})
}

func indexDeleteName(del *struct {
	IndexName string `json:"IndexName"`
}) string {
	if del == nil {
		return ""
	}

	return del.IndexName
}

// applyThroughput applies a billing/throughput change when one was requested.
// A provider that does not expose the capability is a no-op (the change simply
// isn't modeled), so unrelated UpdateTable fields still succeed.
func (h *Handler) applyThroughput(ctx context.Context, table, billingMode string, pt *struct {
	ReadCapacityUnits  int64 `json:"ReadCapacityUnits"`
	WriteCapacityUnits int64 `json:"WriteCapacityUnits"`
}) error {
	if billingMode == "" && pt == nil {
		return nil
	}

	updater, ok := h.db.(throughputUpdater)
	if !ok {
		return nil
	}

	var rcu, wcu int64
	if pt != nil {
		rcu, wcu = pt.ReadCapacityUnits, pt.WriteCapacityUnits
	}

	return updater.UpdateThroughput(ctx, table, billingMode, rcu, wcu)
}

// attrDefJSON is one AttributeDefinitions entry on an UpdateTable request.
type attrDefJSON struct {
	AttributeName string `json:"AttributeName"`
	AttributeType string `json:"AttributeType"`
}

// gsiUpdateJSON is one GlobalSecondaryIndexUpdates entry: a GSI Create or Delete.
type gsiUpdateJSON struct {
	Create *secondaryIndexJSON `json:"Create,omitempty"`
	Delete *struct {
		IndexName string `json:"IndexName"`
	} `json:"Delete,omitempty"`
}

// validateGSIUpdates checks every GSI Create in an UpdateTable request against
// the request's AttributeDefinitions, matching the AWS rule that a new index's
// key attributes must be declared there.
func validateGSIUpdates(updates []gsiUpdateJSON, defs []attrDefJSON) error {
	defined := make(map[string]struct{}, len(defs))
	for _, ad := range defs {
		defined[ad.AttributeName] = struct{}{}
	}

	for i := range updates {
		if err := validateGSICreateAttrs(updates[i].Create, defined); err != nil {
			return err
		}
	}

	return nil
}

// validateGSICreateAttrs enforces that a GSI added via UpdateTable declares its
// key attributes in the request's AttributeDefinitions. AWS rejects a missing
// definition with a ValidationException carrying the wording matched here.
func validateGSICreateAttrs(create *secondaryIndexJSON, defined map[string]struct{}) error {
	if create == nil {
		return nil
	}

	pk, sk := indexKeys(*create)
	for _, name := range []string{pk, sk} {
		if name == "" {
			continue
		}

		if _, ok := defined[name]; !ok {
			return cerrors.Newf(cerrors.InvalidArgument,
				"One or more parameter values were invalid: "+
					"Some index key attributes are not defined in AttributeDefinitions. Missing attributes: %s", name)
		}
	}

	return nil
}

// applyGSIUpdate creates or deletes one GSI as part of an UpdateTable request.
func (h *Handler) applyGSIUpdate(ctx context.Context, table string, create *secondaryIndexJSON, deleteName string) error {
	if create != nil {
		pk, sk := indexKeys(*create)
		_, err := h.db.CreateIndex(ctx, table, dbdriver.GSIConfig{
			Name: create.IndexName, PartitionKey: pk, SortKey: sk, Projection: create.Projection.ProjectionType,
		})

		return err
	}

	if deleteName != "" {
		return h.db.DeleteIndex(ctx, table, deleteName)
	}

	return nil
}

// updateContinuousBackups handles UpdateContinuousBackups, toggling point-in-
// time recovery. Without it PITR can never be enabled and
// DescribeContinuousBackups always reports DISABLED.
func (h *Handler) updateContinuousBackups(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName                        string `json:"TableName"`
		PointInTimeRecoverySpecification struct {
			PointInTimeRecoveryEnabled bool `json:"PointInTimeRecoveryEnabled"`
		} `json:"PointInTimeRecoverySpecification"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	if _, err := h.db.DescribeTable(r.Context(), req.TableName); err != nil {
		writeErr(w, err)
		return
	}

	enabled := req.PointInTimeRecoverySpecification.PointInTimeRecoveryEnabled

	if ctrl, ok := h.db.(pitrController); ok {
		if err := ctrl.SetPITR(r.Context(), req.TableName, enabled); err != nil {
			writeErr(w, err)
			return
		}
	}

	pitr := statusDisabled
	if enabled {
		pitr = statusEnabled
	}

	wire.WriteJSON(w, map[string]any{
		"ContinuousBackupsDescription": map[string]any{
			"ContinuousBackupsStatus": statusEnabled,
			"PointInTimeRecoveryDescription": map[string]any{
				"PointInTimeRecoveryStatus": pitr,
			},
		},
	})
}

// pitrEnabled reports whether point-in-time recovery is on for a table, via the
// optional pitrController capability. A provider without it reports disabled.
func (h *Handler) pitrEnabled(ctx context.Context, table string) bool {
	ctrl, ok := h.db.(pitrController)
	if !ok {
		return false
	}

	on, err := ctrl.GetPITR(ctx, table)

	return err == nil && on
}
