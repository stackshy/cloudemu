package driver

import "context"

// Kinesis streaming-destination status constants. A destination is synchronous
// in the emulator, so an enable lands ACTIVE immediately and a disable lands
// DISABLED immediately.
const (
	KinesisStatusActive   = "ACTIVE"
	KinesisStatusDisabled = "DISABLED"
)

// KinesisDestination describes one Kinesis Data Streams destination attached to
// a table by EnableKinesisStreamingDestination. StreamArn is supplied by the
// caller and stored verbatim; Precision is the ApproximateCreationDateTimePrecision
// ("MILLISECOND" or "MICROSECOND") the destination records with, empty when the
// caller left it unset.
type KinesisDestination struct {
	StreamArn string
	Status    string // KinesisStatusActive / KinesisStatusDisabled
	Precision string // ApproximateCreationDateTimePrecision, optional
}

// KinesisStreamer is an OPTIONAL capability, discovered by type assertion (like
// Backuper): a provider whose tables can stream item-level changes to Kinesis
// Data Streams implements it. Only the AWS DynamoDB mock does; Cosmos DB /
// Firestore don't, so it stays off the cross-cloud Database interface.
type KinesisStreamer interface {
	// EnableKinesisStreamingDestination attaches streamArn to the table (or
	// re-activates it) and returns the resulting destination.
	EnableKinesisStreamingDestination(ctx context.Context, table, streamArn, precision string) (KinesisDestination, error)
	// DisableKinesisStreamingDestination marks the table's streamArn destination
	// DISABLED and returns it.
	DisableKinesisStreamingDestination(ctx context.Context, table, streamArn string) (KinesisDestination, error)
	// UpdateKinesisStreamingDestination changes the precision of the table's
	// streamArn destination and returns it.
	UpdateKinesisStreamingDestination(ctx context.Context, table, streamArn, precision string) (KinesisDestination, error)
	// DescribeKinesisStreamingDestination returns every destination attached to
	// the table, ordered by StreamArn.
	DescribeKinesisStreamingDestination(ctx context.Context, table string) ([]KinesisDestination, error)
}
