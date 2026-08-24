package dynamodb

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire"
	dbdriver "github.com/stackshy/cloudemu/v2/services/database/driver"
)

// streamsTargetPrefix identifies the DynamoDB Streams data-plane API. It shares
// the DynamoDB host/port but uses a distinct X-Amz-Target prefix, so its Matches
// predicate is disjoint from the control-plane DynamoDB_20120810.* handler.
const streamsTargetPrefix = "DynamoDBStreams_20120810."

const (
	streamShardID     = "shard-000"
	streamEventSource = "aws:dynamodb"
	streamEventVer    = "1.1"
	defaultAWSRegion  = "us-east-1"
	maxGetRecords     = 1000
)

// Shard iterator types accepted by GetShardIterator.
const (
	iterTrimHorizon = "TRIM_HORIZON"
	iterLatest      = "LATEST"
	iterAtSeq       = "AT_SEQUENCE_NUMBER"
	iterAfterSeq    = "AFTER_SEQUENCE_NUMBER"
)

// StreamsHandler serves the DynamoDB Streams data-plane protocol
// (DynamoDBStreams_20120810.*) against a database.Database driver. It reuses the
// change-record buffer the driver already maintains for item writes; no new
// provider state or shared driver method is introduced.
type StreamsHandler struct {
	db dbdriver.Database
}

// NewStreams returns a DynamoDB Streams handler backed by db.
func NewStreams(db dbdriver.Database) *StreamsHandler {
	return &StreamsHandler{db: db}
}

// Matches returns true for DynamoDB Streams requests, identified by an
// X-Amz-Target header of "DynamoDBStreams_20120810.<Operation>". This prefix is
// disjoint from DynamoDB_20120810.* (control plane) and AmazonSQS.*.
func (*StreamsHandler) Matches(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("X-Amz-Target"), streamsTargetPrefix)
}

// ServeHTTP dispatches DynamoDB Streams operations based on X-Amz-Target.
func (h *StreamsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.Header.Get("X-Amz-Target"), streamsTargetPrefix)

	switch op {
	case "ListStreams":
		h.listStreams(w, r)
	case "DescribeStream":
		h.describeStream(w, r)
	case "GetShardIterator":
		h.getShardIterator(w, r)
	case "GetRecords":
		h.getRecords(w, r)
	default:
		wire.WriteJSONError(w, http.StatusBadRequest,
			"UnknownOperationException", "unknown operation: "+op)
	}
}

// resolveStream maps a stream ARN back to the owning table's config by scanning
// the tables and matching StreamArn. DynamoDB has no ARN-lookup driver method
// and a table carries at most one active stream, so an O(n) scan is acceptable.
// It returns a ResourceNotFoundException-mapped error when no enabled stream
// matches, matching real DynamoDB.
func (h *StreamsHandler) resolveStream(ctx context.Context, streamArn string) (*dbdriver.TableConfig, error) {
	tables, err := h.db.ListTables(ctx)
	if err != nil {
		return nil, err
	}

	for _, name := range tables {
		cfg, derr := h.db.DescribeTable(ctx, name)
		if derr != nil {
			continue
		}

		if cfg.StreamEnabled && cfg.StreamArn == streamArn {
			return cfg, nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "stream %s not found", streamArn)
}

func (h *StreamsHandler) listStreams(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TableName string `json:"TableName"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	tables, err := h.db.ListTables(r.Context())
	if err != nil {
		writeStreamsErr(w, err)
		return
	}

	streams := make([]map[string]any, 0)

	for _, name := range tables {
		if req.TableName != "" && name != req.TableName {
			continue
		}

		cfg, derr := h.db.DescribeTable(r.Context(), name)
		if derr != nil || !cfg.StreamEnabled {
			continue
		}

		streams = append(streams, map[string]any{
			"StreamArn":   cfg.StreamArn,
			"TableName":   cfg.Name,
			"StreamLabel": cfg.StreamLabel,
		})
	}

	wire.WriteJSON(w, map[string]any{"Streams": streams})
}

func (h *StreamsHandler) describeStream(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamArn string `json:"StreamArn"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	cfg, err := h.resolveStream(r.Context(), req.StreamArn)
	if err != nil {
		writeStreamsErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{"StreamDescription": h.streamDescription(r.Context(), cfg)})
}

// streamDescription builds the StreamDescription wire shape. The emulator models
// a single always-open shard (matching the driver's fixed shard id), whose
// SequenceNumberRange has a StartingSequenceNumber and no EndingSequenceNumber.
func (h *StreamsHandler) streamDescription(ctx context.Context, cfg *dbdriver.TableConfig) map[string]any {
	keySchema := []map[string]string{{"AttributeName": cfg.PartitionKey, "KeyType": keyTypeHash}}
	if cfg.SortKey != "" {
		keySchema = append(keySchema, map[string]string{"AttributeName": cfg.SortKey, "KeyType": keyTypeRange})
	}

	seqRange := map[string]any{"StartingSequenceNumber": h.startingSequence(ctx, cfg.Name)}

	return map[string]any{
		"StreamArn":               cfg.StreamArn,
		"StreamLabel":             cfg.StreamLabel,
		"StreamStatus":            statusEnabled,
		"StreamViewType":          cfg.StreamViewType,
		"CreationRequestDateTime": cfg.CreatedAtUnix,
		"TableName":               cfg.Name,
		"KeySchema":               keySchema,
		"Shards": []map[string]any{{
			"ShardId":             streamShardID,
			"SequenceNumberRange": seqRange,
		}},
	}
}

// startingSequence returns the sequence number of the oldest record still in the
// (ring-buffered) stream, or "0" when the stream is empty.
func (h *StreamsHandler) startingSequence(ctx context.Context, table string) string {
	it, err := h.db.GetStreamRecords(ctx, table, 1, "")
	if err != nil || len(it.Records) == 0 {
		return "0"
	}

	return it.Records[0].SequenceNumber
}

func (h *StreamsHandler) getShardIterator(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StreamArn         string `json:"StreamArn"`
		ShardID           string `json:"ShardId"`
		ShardIteratorType string `json:"ShardIteratorType"`
		SequenceNumber    string `json:"SequenceNumber"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	cfg, err := h.resolveStream(r.Context(), req.StreamArn)
	if err != nil {
		writeStreamsErr(w, err)
		return
	}

	after, err := h.iteratorPosition(r.Context(), cfg.Name, req.ShardIteratorType, req.SequenceNumber)
	if err != nil {
		writeStreamsErr(w, err)
		return
	}

	wire.WriteJSON(w, map[string]any{
		"ShardIterator": encodeIterator(streamCursor{Table: cfg.Name, After: after, Shard: streamShardID}),
	})
}

// iteratorPosition resolves an iterator type into the "after" token consumed by
// the driver's after-sequence buffer read. TRIM_HORIZON starts at the beginning
// (empty token); LATEST starts past the current tip; AFTER_SEQUENCE_NUMBER uses
// the named sequence directly; AT_SEQUENCE_NUMBER must include the named record,
// so it seeks to the predecessor (sequence numbers are contiguous integers).
func (h *StreamsHandler) iteratorPosition(
	ctx context.Context, table, iterType, seq string,
) (string, error) {
	switch iterType {
	case iterTrimHorizon:
		return "", nil
	case iterLatest:
		return h.tipSequence(ctx, table)
	case iterAfterSeq:
		return seq, nil
	case iterAtSeq:
		return predecessorSequence(seq), nil
	default:
		return "", cerrors.Newf(cerrors.InvalidArgument, "invalid ShardIteratorType %q", iterType)
	}
}

// tipSequence returns the sequence number of the newest record currently in the
// stream, so a LATEST iterator only sees records written after it was minted.
// An empty stream yields the empty token (start from the beginning, which is
// also the tip).
func (h *StreamsHandler) tipSequence(ctx context.Context, table string) (string, error) {
	after := ""

	for {
		it, err := h.db.GetStreamRecords(ctx, table, maxGetRecords, after)
		if err != nil {
			return "", err
		}

		if len(it.Records) > 0 {
			after = it.Records[len(it.Records)-1].SequenceNumber
		}

		if it.NextToken == "" {
			return after, nil
		}
	}
}

// predecessorSequence returns the sequence token that, fed to the driver's
// after-sequence read, yields the named record inclusively. Sequence numbers are
// contiguous positive integers, so the predecessor is seq-1; a non-numeric or
// first sequence falls back to the beginning of the stream.
func predecessorSequence(seq string) string {
	n, err := strconv.ParseInt(seq, 10, 64)
	if err != nil || n <= 1 {
		return ""
	}

	return strconv.FormatInt(n-1, 10)
}

func (h *StreamsHandler) getRecords(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ShardIterator string `json:"ShardIterator"`
		Limit         int    `json:"Limit"`
	}

	if !wire.DecodeJSON(w, r, &req) {
		return
	}

	cur, err := decodeIterator(req.ShardIterator)
	if err != nil {
		wire.WriteJSONError(w, http.StatusBadRequest,
			"ExpiredIteratorException", "invalid shard iterator")

		return
	}

	it, err := h.db.GetStreamRecords(r.Context(), cur.Table, req.Limit, cur.After)
	if err != nil {
		writeStreamsErr(w, err)
		return
	}

	region := regionFromTable(r.Context(), h.db, cur.Table)

	records := make([]map[string]any, 0, len(it.Records))
	for i := range it.Records {
		records = append(records, streamRecordWire(&it.Records[i], region))
	}

	// The single shard is always open, so a next iterator is always returned
	// (never null); it advances past the last delivered record so repeated polls
	// drain new writes.
	next := cur
	if n := len(it.Records); n > 0 {
		next.After = it.Records[n-1].SequenceNumber
	}

	wire.WriteJSON(w, map[string]any{
		"Records":           records,
		"NextShardIterator": encodeIterator(next),
	})
}

// streamRecordWire renders a driver StreamRecord into the API_streams_Record
// wire shape, AV-encoding the key/image maps via the shared codec.
func streamRecordWire(rec *dbdriver.StreamRecord, region string) map[string]any {
	dyn := map[string]any{
		"ApproximateCreationDateTime": float64(rec.Timestamp.Unix()),
		"Keys":                        toWireItem(rec.Keys),
		"SequenceNumber":              rec.SequenceNumber,
		"SizeBytes":                   recordSizeBytes(rec),
		"StreamViewType":              viewTypeForRecord(rec),
	}

	if rec.NewImage != nil {
		dyn["NewImage"] = toWireItem(rec.NewImage)
	}

	if rec.OldImage != nil {
		dyn["OldImage"] = toWireItem(rec.OldImage)
	}

	return map[string]any{
		"awsRegion":    region,
		"eventID":      rec.EventID,
		"eventName":    rec.EventType,
		"eventSource":  streamEventSource,
		"eventVersion": streamEventVer,
		"dynamodb":     dyn,
	}
}

// viewTypeForRecord infers the record's StreamViewType from the images present,
// so KEYS_ONLY records omit both images while NEW/OLD/NEW_AND_OLD reflect what
// the table's configured view type captured.
func viewTypeForRecord(rec *dbdriver.StreamRecord) string {
	switch {
	case rec.NewImage != nil && rec.OldImage != nil:
		return "NEW_AND_OLD_IMAGES"
	case rec.NewImage != nil:
		return "NEW_IMAGE"
	case rec.OldImage != nil:
		return "OLD_IMAGE"
	default:
		return "KEYS_ONLY"
	}
}

// recordSizeBytes estimates the record's on-the-wire size, as real DynamoDB
// reports it on each stream record. The exact figure is opaque to clients; a
// JSON-encoded byte count of the captured images/keys is a faithful proxy.
func recordSizeBytes(rec *dbdriver.StreamRecord) int {
	size := 0

	for _, m := range []map[string]any{rec.Keys, rec.NewImage, rec.OldImage} {
		if m == nil {
			continue
		}

		if b, err := json.Marshal(m); err == nil {
			size += len(b)
		}
	}

	return size
}

// regionFromTable extracts the AWS region embedded in the table's stream ARN
// (arn:aws:dynamodb:<region>:<account>:table/...). It falls back to the default
// region when the ARN is unavailable or malformed.
func regionFromTable(ctx context.Context, db dbdriver.Database, table string) string {
	cfg, err := db.DescribeTable(ctx, table)
	if err != nil {
		return defaultAWSRegion
	}

	parts := strings.Split(cfg.StreamArn, ":")
	if len(parts) >= 4 && parts[3] != "" {
		return parts[3]
	}

	return defaultAWSRegion
}

// streamCursor is the opaque state a shard iterator carries: which table's
// buffer to read and the after-sequence position within it. It is base64-encoded
// into the ShardIterator token the client treats as opaque.
type streamCursor struct {
	Table string `json:"t"`
	After string `json:"a"`
	Shard string `json:"s"`
}

func encodeIterator(c streamCursor) string {
	// A struct of strings never fails to marshal.
	b, _ := json.Marshal(c)

	return base64.URLEncoding.EncodeToString(b)
}

func decodeIterator(token string) (streamCursor, error) {
	var c streamCursor

	raw, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return c, err
	}

	if err := json.Unmarshal(raw, &c); err != nil {
		return c, err
	}

	return c, nil
}

// writeStreamsErr maps CloudEmu errors to DynamoDB Streams HTTP error responses.
// A missing table/stream and a disabled stream both surface as
// ResourceNotFoundException, matching real DynamoDB Streams.
func writeStreamsErr(w http.ResponseWriter, err error) {
	msg := errMessage(err)

	switch {
	case cerrors.IsNotFound(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", msg)
	case cerrors.IsFailedPrecondition(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ResourceNotFoundException", msg)
	case cerrors.IsInvalidArgument(err):
		wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", msg)
	default:
		wire.WriteJSONError(w, http.StatusInternalServerError, "InternalServerError", msg)
	}
}
