package keyspaces

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/keyspaces"
	"github.com/aws/aws-sdk-go-v2/service/keyspaces/types"

	"github.com/stackshy/cloudemu/v2/server/wire"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

// extractRestoreTimestamp reads the request body, removes the epoch-number
// restoreTimestamp field (which cannot decode into a time.Time), and returns
// the resolved timestamp (defaulting to now when absent) plus the remaining
// body for normal decoding. It writes an error and returns ok=false on failure.
func extractRestoreTimestamp(w http.ResponseWriter, r *http.Request) (time.Time, []byte, bool) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		wire.WriteJSONError(w, http.StatusBadRequest, "SerializationException", "cannot read body: "+err.Error())
		return time.Time{}, nil, false
	}

	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		wire.WriteJSONError(w, http.StatusBadRequest, "SerializationException", "invalid JSON: "+err.Error())
		return time.Time{}, nil, false
	}

	at := time.Now().UTC()

	if tok, present := fields["restoreTimestamp"]; present {
		var epoch float64
		if err = json.Unmarshal(tok, &epoch); err != nil {
			wire.WriteJSONError(w, http.StatusBadRequest, "SerializationException", "invalid restoreTimestamp")
			return time.Time{}, nil, false
		}

		at = time.Unix(int64(epoch), 0).UTC()

		delete(fields, "restoreTimestamp")
	}

	rest, err := json.Marshal(fields)
	if err != nil {
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalServerException", err.Error())
		return time.Time{}, nil, false
	}

	return at, rest, true
}

func (h *Handler) createTable(w http.ResponseWriter, r *http.Request) {
	var in keyspaces.CreateTableInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	cfg := ksdriver.CreateTableConfig{
		KeyspaceName:            aws.ToString(in.KeyspaceName),
		Name:                    aws.ToString(in.TableName),
		SchemaDefinition:        fromWireSchema(in.SchemaDefinition),
		CapacitySpecification:   fromWireCapacity(in.CapacitySpecification),
		EncryptionSpecification: fromWireEncryption(in.EncryptionSpecification),
		DefaultTimeToLive:       int(aws.ToInt32(in.DefaultTimeToLive)),
		ReplicaRegions:          replicaRegions(in.ReplicaSpecifications),
		AutoScaling:             fromWireAutoScaling(in.AutoScalingSpecification),
		Tags:                    tagMap(in.Tags),
	}

	if in.PointInTimeRecovery != nil {
		cfg.PointInTimeRecovery = string(in.PointInTimeRecovery.Status)
	}

	if in.Ttl != nil {
		cfg.TTLStatus = string(in.Ttl.Status)
	}

	if in.ClientSideTimestamps != nil {
		cfg.ClientSideTimestamps = string(in.ClientSideTimestamps.Status)
	}

	if in.CdcSpecification != nil {
		cfg.CdcStatus = string(in.CdcSpecification.Status)
	}

	if in.Comment != nil {
		cfg.Comment = aws.ToString(in.Comment.Message)
	}

	t, err := h.db.CreateTable(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, keyspaces.CreateTableOutput{ResourceArn: aws.String(t.ARN)})
}

func (h *Handler) getTable(w http.ResponseWriter, r *http.Request) {
	var in keyspaces.GetTableInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	t, err := h.db.GetTable(r.Context(), aws.ToString(in.KeyspaceName), aws.ToString(in.TableName))
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, tableOutput(t))
}

// tableOutput builds the GetTable response. CreationTimestamp is omitted: AWS
// JSON 1.0 encodes timestamps as epoch numbers, which encoding/json cannot emit
// for a time.Time.
func tableOutput(t *ksdriver.Table) keyspaces.GetTableOutput {
	out := keyspaces.GetTableOutput{
		KeyspaceName:            aws.String(t.KeyspaceName),
		TableName:               aws.String(t.Name),
		ResourceArn:             aws.String(t.ARN),
		Status:                  types.TableStatus(t.Status),
		SchemaDefinition:        toWireSchema(&t.SchemaDefinition),
		CapacitySpecification:   toWireCapacity(t.CapacitySpecification),
		EncryptionSpecification: toWireEncryption(t.EncryptionSpecification),
		DefaultTimeToLive:       aws.Int32(int32(t.DefaultTimeToLive)), //nolint:gosec // mock TTL seconds never overflow int32.
		PointInTimeRecovery:     &types.PointInTimeRecoverySummary{Status: types.PointInTimeRecoveryStatus(t.PointInTimeRecoveryStatus)},
		Ttl:                     &types.TimeToLive{Status: types.TimeToLiveStatus(t.TTLStatus)},
		ClientSideTimestamps:    &types.ClientSideTimestamps{Status: types.ClientSideTimestampsStatus(t.ClientSideTimestamps)},
		CdcSpecification:        &types.CdcSpecificationSummary{Status: types.CdcStatus(t.CdcStatus)},
	}
	if t.Comment != "" {
		out.Comment = &types.Comment{Message: aws.String(t.Comment)}
	}

	return out
}

func (h *Handler) listTables(w http.ResponseWriter, r *http.Request) {
	var in keyspaces.ListTablesInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	all, err := h.db.ListTables(r.Context(), aws.ToString(in.KeyspaceName))
	if err != nil {
		writeErr(w, err)
		return
	}

	page, next, err := paginate(all, in.MaxResults, in.NextToken)
	if err != nil {
		writeErr(w, err)
		return
	}

	out := keyspaces.ListTablesOutput{NextToken: next}
	for i := range page {
		out.Tables = append(out.Tables, toWireTableSummary(&page[i]))
	}

	writeJSON(w, out)
}

func (h *Handler) updateTable(w http.ResponseWriter, r *http.Request) {
	var in keyspaces.UpdateTableInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	cfg := ksdriver.UpdateTableConfig{
		KeyspaceName: aws.ToString(in.KeyspaceName),
		Name:         aws.ToString(in.TableName),
		AddColumns:   fromWireSchema(&types.SchemaDefinition{AllColumns: in.AddColumns}).AllColumns,
		AutoScaling:  fromWireAutoScaling(in.AutoScalingSpecification),
	}

	if in.CapacitySpecification != nil {
		c := fromWireCapacity(in.CapacitySpecification)
		cfg.CapacitySpecification = &c
	}

	if in.PointInTimeRecovery != nil {
		cfg.PointInTimeRecovery = string(in.PointInTimeRecovery.Status)
	}

	if in.Ttl != nil {
		cfg.TTLStatus = string(in.Ttl.Status)
	}

	if in.ClientSideTimestamps != nil {
		cfg.ClientSideTimestamps = string(in.ClientSideTimestamps.Status)
	}

	if in.CdcSpecification != nil {
		cfg.CdcStatus = string(in.CdcSpecification.Status)
	}

	if in.DefaultTimeToLive != nil {
		v := int(aws.ToInt32(in.DefaultTimeToLive))
		cfg.DefaultTimeToLive = &v
	}

	t, err := h.db.UpdateTable(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, keyspaces.UpdateTableOutput{ResourceArn: aws.String(t.ARN)})
}

func (h *Handler) deleteTable(w http.ResponseWriter, r *http.Request) {
	var in keyspaces.DeleteTableInput
	if !wire.DecodeJSON(w, r, &in) {
		return
	}

	if err := h.db.DeleteTable(r.Context(), aws.ToString(in.KeyspaceName), aws.ToString(in.TableName)); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, keyspaces.DeleteTableOutput{})
}

func (h *Handler) restoreTable(w http.ResponseWriter, r *http.Request) {
	// restoreTimestamp arrives as an AWS-JSON epoch number, which can't decode
	// into the SDK input's time.Time. Pull it out (it's optional — default to
	// now) and decode the rest of the body normally.
	restoreAt, body, ok := extractRestoreTimestamp(w, r)
	if !ok {
		return
	}

	var in keyspaces.RestoreTableInput
	if err := json.Unmarshal(body, &in); err != nil {
		wire.WriteJSONError(w, http.StatusBadRequest, "SerializationException", "invalid JSON: "+err.Error())
		return
	}

	cfg := ksdriver.RestoreTableConfig{
		SourceKeyspace:   aws.ToString(in.SourceKeyspaceName),
		SourceTable:      aws.ToString(in.SourceTableName),
		TargetKeyspace:   aws.ToString(in.TargetKeyspaceName),
		TargetTable:      aws.ToString(in.TargetTableName),
		RestoreTimestamp: restoreAt,
		Tags:             tagMap(in.TagsOverride),
	}

	if in.CapacitySpecificationOverride != nil {
		c := fromWireCapacity(in.CapacitySpecificationOverride)
		cfg.CapacitySpecification = &c
	}

	if in.EncryptionSpecificationOverride != nil {
		e := fromWireEncryption(in.EncryptionSpecificationOverride)
		cfg.EncryptionSpecification = &e
	}

	if in.PointInTimeRecoveryOverride != nil {
		cfg.PointInTimeRecovery = string(in.PointInTimeRecoveryOverride.Status)
	}

	t, err := h.db.RestoreTable(r.Context(), cfg)
	if err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, keyspaces.RestoreTableOutput{RestoredTableARN: aws.String(t.ARN)})
}
