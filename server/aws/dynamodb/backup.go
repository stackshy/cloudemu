package dynamodb

import (
	"context"
	"net/http"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

// pitrWindower is the optional provider capability reporting a table's
// continuous-backup restorable window (earliest/latest restorable time). A
// provider without it simply omits those fields from DescribeContinuousBackups.
type pitrWindower interface {
	PITRWindow(ctx context.Context, table string) (earliest, latest float64, err error)
}

// routeBackups dispatches the on-demand backup and PITR-restore operations.
// Without them CreateBackup/RestoreTableFromBackup/RestoreTableToPointInTime and
// friends return UnknownOperationException and no DR flow works.
func (h *Handler) routeBackups(w http.ResponseWriter, r *http.Request, op string) bool {
	switch op {
	case "CreateBackup":
		h.createBackup(w, r)
	case "DescribeBackup":
		h.describeBackup(w, r)
	case "ListBackups":
		h.listBackups(w, r)
	case "DeleteBackup":
		h.deleteBackup(w, r)
	case "RestoreTableFromBackup":
		h.restoreTableFromBackup(w, r)
	case "RestoreTableToPointInTime":
		h.restoreTableToPointInTime(w, r)
	default:
		return false
	}

	return true
}

// backuper returns the driver's Backuper capability, writing an error response
// and returning false when the driver does not implement it.
func (h *Handler) backuper(w http.ResponseWriter) (dbdriver.Backuper, bool) {
	b, ok := h.db.(dbdriver.Backuper)
	if !ok {
		wire.WriteJSONError(w, http.StatusBadRequest,
			"UnknownOperationException", "backups are not supported by this driver")

		return nil, false
	}

	return b, true
}

func (h *Handler) createBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName  string `json:"TableName"`
		BackupName string `json:"BackupName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	b, ok := h.backuper(w)
	if !ok {
		return
	}

	info, err := b.CreateBackup(r.Context(), req.TableName, req.BackupName)
	if err != nil {
		writeBackupErr(w, err, "TableNotFoundException")
		return
	}

	wire.WriteJSON(w, map[string]any{"BackupDetails": backupDetails(&info)})
}

func (h *Handler) describeBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BackupArn string `json:"BackupArn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	b, ok := h.backuper(w)
	if !ok {
		return
	}

	info, err := b.DescribeBackup(r.Context(), req.BackupArn)
	if err != nil {
		writeBackupErr(w, err, "BackupNotFoundException")
		return
	}

	wire.WriteJSON(w, map[string]any{"BackupDescription": backupDescription(&info)})
}

func (h *Handler) listBackups(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName               string `json:"TableName"`
		Limit                   *int   `json:"Limit"`
		ExclusiveStartBackupArn string `json:"ExclusiveStartBackupArn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	b, ok := h.backuper(w)
	if !ok {
		return
	}

	infos, err := b.ListBackups(r.Context(), req.TableName)
	if err != nil {
		writeErr(w, err)
		return
	}

	infos = backupsAfter(infos, req.ExclusiveStartBackupArn)
	wire.WriteJSON(w, listBackupsResponse(infos, req.Limit))
}

// backupsAfter drops every backup whose ARN is at or before start, resuming an
// ExclusiveStartBackupArn page; infos is already ordered by ARN ascending.
func backupsAfter(infos []dbdriver.BackupInfo, start string) []dbdriver.BackupInfo {
	if start == "" {
		return infos
	}

	for i := range infos {
		if infos[i].BackupArn > start {
			return infos[i:]
		}
	}

	return nil
}

// listBackupsResponse renders one ListBackups page, setting LastEvaluatedBackupArn
// when Limit truncates the result.
func listBackupsResponse(infos []dbdriver.BackupInfo, limit *int) map[string]any {
	truncated := limit != nil && *limit > 0 && *limit < len(infos)
	if truncated {
		infos = infos[:*limit]
	}

	summaries := make([]map[string]any, 0, len(infos))
	for i := range infos {
		summaries = append(summaries, backupSummary(&infos[i]))
	}

	resp := map[string]any{"BackupSummaries": summaries}
	if truncated && len(infos) > 0 {
		resp["LastEvaluatedBackupArn"] = infos[len(infos)-1].BackupArn
	}

	return resp
}

func (h *Handler) deleteBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BackupArn string `json:"BackupArn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	b, ok := h.backuper(w)
	if !ok {
		return
	}

	info, err := b.DeleteBackup(r.Context(), req.BackupArn)
	if err != nil {
		writeBackupErr(w, err, "BackupNotFoundException")
		return
	}

	wire.WriteJSON(w, map[string]any{"BackupDescription": backupDescription(&info)})
}

func (h *Handler) restoreTableFromBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TargetTableName string `json:"TargetTableName"`
		BackupArn       string `json:"BackupArn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	b, ok := h.backuper(w)
	if !ok {
		return
	}

	if err := b.RestoreTableFromBackup(r.Context(), req.BackupArn, req.TargetTableName); err != nil {
		writeBackupErr(w, err, "BackupNotFoundException")
		return
	}

	h.writeRestoredTable(w, r, req.TargetTableName)
}

func (h *Handler) restoreTableToPointInTime(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SourceTableName         string `json:"SourceTableName"`
		TargetTableName         string `json:"TargetTableName"`
		UseLatestRestorableTime bool   `json:"UseLatestRestorableTime"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	b, ok := h.backuper(w)
	if !ok {
		return
	}

	err := b.RestoreTableToPointInTime(r.Context(), req.SourceTableName, req.TargetTableName, req.UseLatestRestorableTime)
	if err != nil {
		writeBackupErr(w, err, "TableNotFoundException")
		return
	}

	h.writeRestoredTable(w, r, req.TargetTableName)
}

// writeRestoredTable re-describes a freshly restored table and returns it as the
// TableDescription the restore operations echo.
func (h *Handler) writeRestoredTable(w http.ResponseWriter, r *http.Request, table string) {
	full, err := h.db.DescribeTable(r.Context(), table)
	if err != nil {
		writeErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"TableDescription": tableDescription(full)})
}

// pitrRecoveryDescription builds the PointInTimeRecoveryDescription block. When
// PITR is enabled it also carries the restorable window (earliest/latest) via
// the optional pitrWindower capability.
func (h *Handler) pitrRecoveryDescription(ctx context.Context, table string, enabled bool) map[string]any {
	status := statusDisabled
	if enabled {
		status = statusEnabled
	}

	desc := map[string]any{"PointInTimeRecoveryStatus": status}

	if enabled {
		if wdw, ok := h.db.(pitrWindower); ok {
			if earliest, latest, err := wdw.PITRWindow(ctx, table); err == nil {
				desc["EarliestRestorableDateTime"] = earliest
				desc["LatestRestorableDateTime"] = latest
			}
		}
	}

	return desc
}

// writeBackupErr maps a provider error to the DynamoDB-specific exception the
// caller expects: a not-found becomes notFoundEx (BackupNotFoundException or
// TableNotFoundException depending on the operation), an already-exists becomes
// TableAlreadyExistsException, and a failed-precondition (PITR off) becomes
// PointInTimeRecoveryUnavailableException.
func writeBackupErr(w http.ResponseWriter, err error, notFoundEx string) {
	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, notFoundEx, errMessage(err))
	case cerrors.IsAlreadyExists(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "TableAlreadyExistsException", errMessage(err))
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "PointInTimeRecoveryUnavailableException", errMessage(err))
	default:
		writeErr(w, err)
	}
}

// backupDetails renders the BackupDetails block common to CreateBackup,
// DescribeBackup and DeleteBackup responses.
func backupDetails(info *dbdriver.BackupInfo) map[string]any {
	return map[string]any{
		"BackupArn":              info.BackupArn,
		"BackupName":             info.BackupName,
		"BackupSizeBytes":        info.SizeBytes,
		"BackupStatus":           info.Status,
		"BackupType":             info.Type,
		"BackupCreationDateTime": info.CreatedUnix,
	}
}

// backupSummary renders one ListBackups BackupSummaries entry.
func backupSummary(info *dbdriver.BackupInfo) map[string]any {
	src := info.SourceTable

	return map[string]any{
		"TableName":              src.Name,
		"TableId":                src.TableID,
		"TableArn":               src.TableArn,
		"BackupArn":              info.BackupArn,
		"BackupName":             info.BackupName,
		"BackupCreationDateTime": info.CreatedUnix,
		"BackupStatus":           info.Status,
		"BackupType":             info.Type,
		"BackupSizeBytes":        info.SizeBytes,
	}
}

// backupDescription renders the BackupDescription block (BackupDetails plus the
// source table's schema) DescribeBackup and DeleteBackup return.
func backupDescription(info *dbdriver.BackupInfo) map[string]any {
	src := info.SourceTable

	billing := src.BillingMode
	if billing == "" {
		billing = billingProvisioned
	}

	keySchema := []map[string]string{{"AttributeName": src.PartitionKey, "KeyType": keyTypeHash}}
	if src.SortKey != "" {
		keySchema = append(keySchema, map[string]string{"AttributeName": src.SortKey, "KeyType": keyTypeRange})
	}

	sourceDetails := map[string]any{
		"TableName":             src.Name,
		"TableId":               src.TableID,
		"TableArn":              src.TableArn,
		"TableSizeBytes":        info.SizeBytes,
		"KeySchema":             keySchema,
		"TableCreationDateTime": src.CreatedAtUnix,
		"ItemCount":             info.ItemCount,
		"BillingMode":           billing,
		"ProvisionedThroughput": map[string]any{
			"ReadCapacityUnits":  src.ReadCapacityUnits,
			"WriteCapacityUnits": src.WriteCapacityUnits,
		},
	}

	return map[string]any{
		"BackupDetails":      backupDetails(info),
		"SourceTableDetails": sourceDetails,
	}
}
