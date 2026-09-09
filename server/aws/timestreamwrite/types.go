package timestreamwrite

import (
	"encoding/json"
	"time"

	"github.com/stackshy/cloudemu/v2/services/timestreamwrite/driver"
)

// tagJSON is the wire shape of a Timestream tag: an object with PascalCase
// Key/Value members.
type tagJSON struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func tagsToWire(tags []driver.Tag) []tagJSON {
	if tags == nil {
		return nil
	}

	out := make([]tagJSON, len(tags))
	for i, t := range tags {
		out[i] = tagJSON{Key: t.Key, Value: t.Value}
	}

	return out
}

func tagsFromWire(tags []tagJSON) []driver.Tag {
	if tags == nil {
		return nil
	}

	out := make([]driver.Tag, len(tags))
	for i, t := range tags {
		out[i] = driver.Tag{Key: t.Key, Value: t.Value}
	}

	return out
}

// epochSeconds renders a driver timestamp as the epoch-seconds number
// Timestream's wire uses for CreationTime/LastUpdatedTime.
func epochSeconds(t time.Time) int64 {
	return t.Unix()
}

// retentionJSON is the wire shape of RetentionProperties. Both members are
// required together on the real API.
type retentionJSON struct {
	MemoryStoreRetentionPeriodInHours  int64 `json:"MemoryStoreRetentionPeriodInHours"`
	MagneticStoreRetentionPeriodInDays int64 `json:"MagneticStoreRetentionPeriodInDays"`
}

func retentionToWire(r *driver.RetentionProperties) *retentionJSON {
	if r == nil {
		return nil
	}

	return &retentionJSON{
		MemoryStoreRetentionPeriodInHours:  r.MemoryStoreRetentionPeriodInHours,
		MagneticStoreRetentionPeriodInDays: r.MagneticStoreRetentionPeriodInDays,
	}
}

func retentionFromWire(r *retentionJSON) *driver.RetentionProperties {
	if r == nil {
		return nil
	}

	return &driver.RetentionProperties{
		MemoryStoreRetentionPeriodInHours:  r.MemoryStoreRetentionPeriodInHours,
		MagneticStoreRetentionPeriodInDays: r.MagneticStoreRetentionPeriodInDays,
	}
}

// databaseJSON is the wire shape of a Database, shared by every operation that
// returns one.
type databaseJSON struct {
	Arn             string `json:"Arn"`
	DatabaseName    string `json:"DatabaseName"`
	KmsKeyID        string `json:"KmsKeyId"`
	TableCount      int64  `json:"TableCount"`
	CreationTime    int64  `json:"CreationTime"`
	LastUpdatedTime int64  `json:"LastUpdatedTime"`
}

func toDatabase(d *driver.Database) databaseJSON {
	return databaseJSON{
		Arn:             d.Arn,
		DatabaseName:    d.DatabaseName,
		KmsKeyID:        d.KmsKeyID,
		TableCount:      d.TableCount,
		CreationTime:    epochSeconds(d.CreationTime),
		LastUpdatedTime: epochSeconds(d.LastUpdatedTime),
	}
}

// tableJSON is the wire shape of a Table. The magnetic-store and schema blocks
// are emitted verbatim as raw JSON so they round-trip without drift, and are
// omitted when unset.
type tableJSON struct {
	Arn                          string          `json:"Arn"`
	TableName                    string          `json:"TableName"`
	DatabaseName                 string          `json:"DatabaseName"`
	TableStatus                  string          `json:"TableStatus"`
	RetentionProperties          *retentionJSON  `json:"RetentionProperties,omitempty"`
	MagneticStoreWriteProperties json.RawMessage `json:"MagneticStoreWriteProperties,omitempty"`
	Schema                       json.RawMessage `json:"Schema,omitempty"`
	CreationTime                 int64           `json:"CreationTime"`
	LastUpdatedTime              int64           `json:"LastUpdatedTime"`
}

func toTable(t *driver.Table) tableJSON {
	return tableJSON{
		Arn:                          t.Arn,
		TableName:                    t.TableName,
		DatabaseName:                 t.DatabaseName,
		TableStatus:                  t.TableStatus,
		RetentionProperties:          retentionToWire(t.RetentionProperties),
		MagneticStoreWriteProperties: t.MagneticStoreWriteProperties,
		Schema:                       t.Schema,
		CreationTime:                 epochSeconds(t.CreationTime),
		LastUpdatedTime:              epochSeconds(t.LastUpdatedTime),
	}
}
