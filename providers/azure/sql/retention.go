package sql

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	rdsdriver "github.com/stackshy/cloudemu/v2/services/relationaldb/driver"
)

// Azure defaults for a database that never set its retention policies, and
// for a server's connection policy.
const (
	defaultSTRDays          = 7
	defaultSTRDiffHours     = 12
	minSTRDays              = 1
	maxSTRDays              = 35
	zeroRetention           = "PT0S"
	defaultConnectionPolicy = "Default"
)

var (
	_ rdsdriver.DatabaseRetentionPolicies = (*Mock)(nil)
	_ rdsdriver.ServerConnectionPolicies  = (*Mock)(nil)
)

func (m *Mock) requireDatabase(server, database string) error {
	if _, ok := m.databases.Get(dbKey(server, database)); !ok {
		return cerrors.Newf(cerrors.NotFound, "database %q not found on server %q", database, server)
	}

	return nil
}

// SetShortTermRetention stores a database's short-term retention policy.
// retentionDays must be 1-35 and diffBackupIntervalInHours 12 or 24.
func (m *Mock) SetShortTermRetention(
	_ context.Context, in *rdsdriver.ShortTermRetentionPolicy,
) (*rdsdriver.ShortTermRetentionPolicy, error) {
	p := *in

	if p.RetentionDays == 0 {
		p.RetentionDays = defaultSTRDays
	}

	if p.DiffBackupIntervalInHours == 0 {
		p.DiffBackupIntervalInHours = defaultSTRDiffHours
	}

	if p.RetentionDays < minSTRDays || p.RetentionDays > maxSTRDays {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"retentionDays %d is invalid: it must be between %d and %d", p.RetentionDays, minSTRDays, maxSTRDays)
	}

	if p.DiffBackupIntervalInHours != defaultSTRDiffHours && p.DiffBackupIntervalInHours != 2*defaultSTRDiffHours {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"diffBackupIntervalInHours %d is invalid: it must be 12 or 24", p.DiffBackupIntervalInHours)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireDatabase(p.Server, p.Database); err != nil {
		return nil, err
	}

	m.str.Set(dbKey(p.Server, p.Database), p)

	return &p, nil
}

// GetShortTermRetention returns a database's policy, or the default.
func (m *Mock) GetShortTermRetention(
	_ context.Context, server, database string,
) (*rdsdriver.ShortTermRetentionPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.requireDatabase(server, database); err != nil {
		return nil, err
	}

	p, ok := m.str.Get(dbKey(server, database))
	if !ok {
		p = rdsdriver.ShortTermRetentionPolicy{Server: server, Database: database,
			RetentionDays: defaultSTRDays, DiffBackupIntervalInHours: defaultSTRDiffHours}
	}

	return &p, nil
}

// SetLongTermRetention stores a database's long-term retention policy; the
// ISO-8601 durations are kept verbatim and empty ones default to PT0S.
func (m *Mock) SetLongTermRetention(
	_ context.Context, in *rdsdriver.LongTermRetentionPolicy,
) (*rdsdriver.LongTermRetentionPolicy, error) {
	p := *in

	for _, d := range []*string{&p.WeeklyRetention, &p.MonthlyRetention, &p.YearlyRetention} {
		if *d == "" {
			*d = zeroRetention
		}
	}

	if p.WeekOfYear == 0 {
		p.WeekOfYear = 1
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.requireDatabase(p.Server, p.Database); err != nil {
		return nil, err
	}

	m.ltr.Set(dbKey(p.Server, p.Database), p)

	return &p, nil
}

// GetLongTermRetention returns a database's policy, or the default.
func (m *Mock) GetLongTermRetention(
	_ context.Context, server, database string,
) (*rdsdriver.LongTermRetentionPolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.requireDatabase(server, database); err != nil {
		return nil, err
	}

	p, ok := m.ltr.Get(dbKey(server, database))
	if !ok {
		p = rdsdriver.LongTermRetentionPolicy{Server: server, Database: database, WeeklyRetention: zeroRetention,
			MonthlyRetention: zeroRetention, YearlyRetention: zeroRetention, WeekOfYear: 1}
	}

	return &p, nil
}

// SetConnectionPolicy stores a server's connection type: Default, Proxy or
// Redirect (case-insensitive, stored in canonical case).
func (m *Mock) SetConnectionPolicy(_ context.Context, server, connectionType string) (string, error) {
	canonical := ""

	for _, v := range []string{defaultConnectionPolicy, "Proxy", "Redirect"} {
		if strings.EqualFold(v, connectionType) {
			canonical = v
		}
	}

	if canonical == "" {
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"connectionType %q is invalid: it must be Default, Proxy or Redirect", connectionType)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.clusters.Get(server); !ok {
		return "", cerrors.Newf(cerrors.NotFound, "server %q not found", server)
	}

	m.connPolicies.Set(server, canonical)

	return canonical, nil
}

// GetConnectionPolicy returns a server's connection type, or Default.
func (m *Mock) GetConnectionPolicy(_ context.Context, server string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, ok := m.clusters.Get(server); !ok {
		return "", cerrors.Newf(cerrors.NotFound, "server %q not found", server)
	}

	if v, ok := m.connPolicies.Get(server); ok {
		return v, nil
	}

	return defaultConnectionPolicy, nil
}
