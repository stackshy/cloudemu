package keyspaces

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ksdriver "github.com/stackshy/cloudemu/v2/services/keyspaces/driver"
)

func cloneSchema(s *ksdriver.SchemaDefinition) ksdriver.SchemaDefinition {
	return ksdriver.SchemaDefinition{
		AllColumns:     append([]ksdriver.ColumnDefinition(nil), s.AllColumns...),
		PartitionKeys:  append([]ksdriver.PartitionKey(nil), s.PartitionKeys...),
		ClusteringKeys: append([]ksdriver.ClusteringKey(nil), s.ClusteringKeys...),
		StaticColumns:  append([]ksdriver.StaticColumn(nil), s.StaticColumns...),
	}
}

func cloneAutoScaling(a *ksdriver.AutoScalingSpecification) *ksdriver.AutoScalingSpecification {
	if a == nil {
		return nil
	}

	out := ksdriver.AutoScalingSpecification{}

	if a.Read != nil {
		r := *a.Read
		out.Read = &r
	}

	if a.Write != nil {
		w := *a.Write
		out.Write = &w
	}

	return &out
}

func cloneTable(in *ksdriver.Table) ksdriver.Table {
	t := *in
	t.SchemaDefinition = cloneSchema(&t.SchemaDefinition)
	t.ReplicaRegions = append([]string(nil), t.ReplicaRegions...)
	t.AutoScaling = cloneAutoScaling(t.AutoScaling)
	t.Tags = copyTags(t.Tags)

	return t
}

// disabledIfEmpty defaults an unset status field to "DISABLED".
func disabledIfEmpty(v string) string {
	if v == "" {
		return "DISABLED"
	}

	return v
}

// validateSchema requires at least one partition key, rejects empty/duplicate
// column names, and rejects partition/clustering/static columns not declared in
// AllColumns.
func declaredColumns(s *ksdriver.SchemaDefinition) (map[string]struct{}, error) {
	declared := make(map[string]struct{}, len(s.AllColumns))

	for _, c := range s.AllColumns {
		if c.Name == "" {
			return nil, cerrors.New(cerrors.InvalidArgument, "column name must not be empty")
		}

		if _, dup := declared[c.Name]; dup {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "duplicate column %q in schema", c.Name)
		}

		declared[c.Name] = struct{}{}
	}

	return declared, nil
}

func validateSchema(s *ksdriver.SchemaDefinition) error {
	if len(s.PartitionKeys) == 0 {
		return cerrors.New(cerrors.InvalidArgument, "schema requires at least one partition key")
	}

	declared, err := declaredColumns(s)
	if err != nil {
		return err
	}

	for _, pk := range s.PartitionKeys {
		if _, ok := declared[pk.Name]; !ok {
			return cerrors.Newf(cerrors.InvalidArgument, "partition key %q is not declared in the schema columns", pk.Name)
		}
	}

	for _, ck := range s.ClusteringKeys {
		if _, ok := declared[ck.Name]; !ok {
			return cerrors.Newf(cerrors.InvalidArgument, "clustering key %q is not declared in the schema columns", ck.Name)
		}
	}

	for _, sc := range s.StaticColumns {
		if _, ok := declared[sc.Name]; !ok {
			return cerrors.Newf(cerrors.InvalidArgument, "static column %q is not declared in the schema columns", sc.Name)
		}
	}

	return nil
}

// CreateTable creates a table in an existing keyspace.
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) CreateTable(_ context.Context, cfg ksdriver.CreateTableConfig) (*ksdriver.Table, error) {
	if err := validName("table", cfg.Name); err != nil {
		return nil, err
	}

	if err := validateSchema(&cfg.SchemaDefinition); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.keyspaces.Has(cfg.KeyspaceName) {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "keyspace %q not found", cfg.KeyspaceName)
	}

	key := tableKey(cfg.KeyspaceName, cfg.Name)
	if m.tables.Has(key) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "table %q already exists in keyspace %q", cfg.Name, cfg.KeyspaceName)
	}

	t := ksdriver.Table{
		KeyspaceName:              cfg.KeyspaceName,
		Name:                      cfg.Name,
		ARN:                       m.tableARN(cfg.KeyspaceName, cfg.Name),
		Status:                    ksdriver.StatusActive,
		SchemaDefinition:          cloneSchema(&cfg.SchemaDefinition),
		CapacitySpecification:     resolveCapacity(cfg.CapacitySpecification),
		EncryptionSpecification:   resolveEncryption(cfg.EncryptionSpecification),
		PointInTimeRecoveryStatus: disabledIfEmpty(cfg.PointInTimeRecovery),
		TTLStatus:                 disabledIfEmpty(cfg.TTLStatus),
		DefaultTimeToLive:         cfg.DefaultTimeToLive,
		ClientSideTimestamps:      disabledIfEmpty(cfg.ClientSideTimestamps),
		CdcStatus:                 disabledIfEmpty(cfg.CdcStatus),
		Comment:                   cfg.Comment,
		ReplicaRegions:            append([]string(nil), cfg.ReplicaRegions...),
		AutoScaling:               cloneAutoScaling(cfg.AutoScaling),
		CreationTimestamp:         m.opts.Clock.Now().UTC(),
	}
	m.tables.Set(key, t)
	m.setTags(t.ARN, cfg.Tags)
	m.registerTableUDTRefs(cfg.KeyspaceName, cfg.Name, &t.SchemaDefinition)

	out := cloneTable(&t)

	return &out, nil
}

func resolveCapacity(c ksdriver.CapacitySpecification) ksdriver.CapacitySpecification {
	if c.ThroughputMode == "" {
		c.ThroughputMode = ksdriver.ThroughputPayPerRequest
	}

	if c.ThroughputMode == ksdriver.ThroughputProvisioned {
		if c.ReadCapacityUnits == 0 {
			c.ReadCapacityUnits = 1
		}

		if c.WriteCapacityUnits == 0 {
			c.WriteCapacityUnits = 1
		}
	}

	return c
}

func resolveEncryption(e ksdriver.EncryptionSpecification) ksdriver.EncryptionSpecification {
	if e.Type == "" {
		e.Type = "AWS_OWNED_KMS_KEY"
	}

	return e
}

func (m *Mock) getTableLocked(keyspace, table string) (ksdriver.Table, error) {
	t, ok := m.tables.Get(tableKey(keyspace, table))
	if !ok {
		return ksdriver.Table{}, cerrors.Newf(cerrors.NotFound, "table %q not found in keyspace %q", table, keyspace)
	}

	return t, nil
}

// GetTable returns a table by keyspace + name.
func (m *Mock) GetTable(_ context.Context, keyspace, table string) (*ksdriver.Table, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	t, err := m.getTableLocked(keyspace, table)
	if err != nil {
		return nil, err
	}

	out := cloneTable(&t)

	return &out, nil
}

// ListTables returns all tables in a keyspace, deterministically ordered.
//
//nolint:dupl // the per-keyspace prefix filter mirrors ListTypes by design.
func (m *Mock) ListTables(_ context.Context, keyspace string) ([]ksdriver.Table, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.keyspaces.Has(keyspace) {
		return nil, cerrors.Newf(cerrors.NotFound, "keyspace %q not found", keyspace)
	}

	prefix := keyspace + "/"
	all := m.tables.SortedValues()
	out := make([]ksdriver.Table, 0, len(all))

	for i := range all {
		if strings.HasPrefix(tableKey(all[i].KeyspaceName, all[i].Name), prefix) {
			out = append(out, cloneTable(&all[i]))
		}
	}

	return out, nil
}

// UpdateTable applies in-place changes; unset fields are preserved.
//
//nolint:gocritic // cfg matches the driver signature; per-field optional updates.
func (m *Mock) UpdateTable(_ context.Context, cfg ksdriver.UpdateTableConfig) (*ksdriver.Table, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, err := m.getTableLocked(cfg.KeyspaceName, cfg.Name)
	if err != nil {
		return nil, err
	}

	existing := make(map[string]struct{}, len(t.SchemaDefinition.AllColumns))
	for _, c := range t.SchemaDefinition.AllColumns {
		existing[c.Name] = struct{}{}
	}

	for _, c := range cfg.AddColumns {
		if _, dup := existing[c.Name]; dup {
			return nil, cerrors.Newf(cerrors.InvalidArgument, "column %q already exists", c.Name)
		}

		existing[c.Name] = struct{}{}
	}

	t.SchemaDefinition.AllColumns = append(t.SchemaDefinition.AllColumns, cfg.AddColumns...)

	if cfg.CapacitySpecification != nil {
		t.CapacitySpecification = resolveCapacity(*cfg.CapacitySpecification)
	}

	t.PointInTimeRecoveryStatus = orKeep(cfg.PointInTimeRecovery, t.PointInTimeRecoveryStatus)
	t.TTLStatus = orKeep(cfg.TTLStatus, t.TTLStatus)
	t.ClientSideTimestamps = orKeep(cfg.ClientSideTimestamps, t.ClientSideTimestamps)
	t.CdcStatus = orKeep(cfg.CdcStatus, t.CdcStatus)
	t.Comment = orKeep(cfg.Comment, t.Comment)

	if cfg.DefaultTimeToLive != nil {
		t.DefaultTimeToLive = *cfg.DefaultTimeToLive
	}

	if cfg.AutoScaling != nil {
		t.AutoScaling = cloneAutoScaling(cfg.AutoScaling)
	}

	m.tables.Set(tableKey(cfg.KeyspaceName, cfg.Name), t)

	out := cloneTable(&t)

	return &out, nil
}

func orKeep(v, cur string) string {
	if v == "" {
		return cur
	}

	return v
}

// DeleteTable removes a table.
func (m *Mock) DeleteTable(_ context.Context, keyspace, table string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := tableKey(keyspace, table)

	t, ok := m.tables.Get(key)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "table %q not found in keyspace %q", table, keyspace)
	}

	m.tables.Delete(key)
	delete(m.tags, t.ARN)
	m.unregisterTableUDTRefs(keyspace, table)

	return nil
}

// RestoreTable creates a new table from a source table's point-in-time state.
//
//nolint:gocritic // cfg matches the driver signature.
func (m *Mock) RestoreTable(_ context.Context, cfg ksdriver.RestoreTableConfig) (*ksdriver.Table, error) {
	if err := validName("table", cfg.TargetTable); err != nil {
		return nil, err
	}

	if cfg.RestoreTimestamp.IsZero() {
		return nil, cerrors.New(cerrors.InvalidArgument, "restoreTimestamp is required")
	}

	if cfg.RestoreTimestamp.After(m.opts.Clock.Now().UTC()) {
		return nil, cerrors.New(cerrors.InvalidArgument, "restoreTimestamp must not be in the future")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	src, err := m.getTableLocked(cfg.SourceKeyspace, cfg.SourceTable)
	if err != nil {
		return nil, err
	}

	if !m.keyspaces.Has(cfg.TargetKeyspace) {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "target keyspace %q not found", cfg.TargetKeyspace)
	}

	targetKey := tableKey(cfg.TargetKeyspace, cfg.TargetTable)
	if m.tables.Has(targetKey) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "table %q already exists in keyspace %q", cfg.TargetTable, cfg.TargetKeyspace)
	}

	restored := cloneTable(&src)
	restored.KeyspaceName = cfg.TargetKeyspace
	restored.Name = cfg.TargetTable
	restored.ARN = m.tableARN(cfg.TargetKeyspace, cfg.TargetTable)
	restored.Status = ksdriver.StatusActive
	restored.CreationTimestamp = m.opts.Clock.Now().UTC()
	restored.Tags = nil

	if cfg.CapacitySpecification != nil {
		restored.CapacitySpecification = resolveCapacity(*cfg.CapacitySpecification)
	}

	if cfg.EncryptionSpecification != nil {
		restored.EncryptionSpecification = resolveEncryption(*cfg.EncryptionSpecification)
	}

	restored.PointInTimeRecoveryStatus = orKeep(cfg.PointInTimeRecovery, restored.PointInTimeRecoveryStatus)

	m.tables.Set(targetKey, restored)
	m.setTags(restored.ARN, cfg.Tags)
	m.registerTableUDTRefs(cfg.TargetKeyspace, cfg.TargetTable, &restored.SchemaDefinition)

	out := cloneTable(&restored)

	return &out, nil
}
