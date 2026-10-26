package cloudwatchlogs

import (
	"context"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// logGroupNamePatternRE is the ListLogGroups logGroupNamePattern constraint:
// up to five 3-24 character alternatives separated by '|', each optionally
// anchored with '^'.
var logGroupNamePatternRE = regexp.MustCompile(
	`^(\^?[\.\-_/#A-Za-z0-9]{3,24})(\|\^?[\.\-_/#A-Za-z0-9]{3,24}){0,4}$`)

// CreateAWSLogGroup creates a log group with the CloudWatch Logs-only
// CreateLogGroup fields. The class defaults to STANDARD and cannot change
// later; a KMS key must be an enabled key ARN.
func (m *Mock) CreateAWSLogGroup(
	ctx context.Context, cfg *driver.LogGroupConfig, opts driver.AWSLogGroupOptions,
) (*driver.AWSLogGroup, error) {
	if err := validateLogGroupName(cfg.Name); err != nil {
		return nil, err
	}

	class, err := resolveLogGroupClass(opts.LogGroupClass)
	if err != nil {
		return nil, err
	}

	if m.groups.Has(cfg.Name) {
		return nil, errors.Newf(errors.AlreadyExists, "log group %q already exists", cfg.Name)
	}

	arn := idgen.AWSARN("logs", regionctx.RegionOr(ctx, m.opts.Region), m.opts.AccountID, "log-group:"+cfg.Name)

	if opts.KmsKeyID != "" {
		if err := m.validateKmsKey(ctx, opts.KmsKeyID, arn); err != nil {
			return nil, err
		}
	}

	// A log group created without a retention policy never expires: AWS leaves
	// retentionInDays unset rather than defaulting to 30 days.
	info := driver.LogGroupInfo{
		Name:          cfg.Name,
		Scope:         cfg.Scope,
		ResourceID:    arn,
		RetentionDays: cfg.RetentionDays,
		CreatedAt:     m.opts.Clock.Now().UTC().Format(time.RFC3339),
		Tags:          maps.Clone(cfg.Tags),
	}

	if info.Tags == nil {
		info.Tags = map[string]string{}
	}

	g := &logGroup{
		info:          info,
		kmsKeyID:      opts.KmsKeyID,
		class:         class,
		streams:       memstore.New[*logStream](),
		metricFilters: memstore.New[*driver.MetricFilterInfo](),
		subFilters:    memstore.New[*driver.SubscriptionFilterInfo](),
	}

	if !m.groups.SetIfAbsent(cfg.Name, g) {
		return nil, errors.Newf(errors.AlreadyExists, "log group %q already exists", cfg.Name)
	}

	out := g.awsView()

	return &out, nil
}

func resolveLogGroupClass(class string) (string, error) {
	switch class {
	case "":
		return driver.LogGroupClassStandard, nil
	case driver.LogGroupClassStandard, driver.LogGroupClassInfrequentAccess, driver.LogGroupClassDelivery:
		return class, nil
	default:
		return "", errors.Newf(errors.InvalidArgument,
			"1 validation error detected: Value '%s' at 'logGroupClass' failed to satisfy constraint: "+
				"Member must satisfy enum value set: [STANDARD, INFREQUENT_ACCESS, DELIVERY]", class)
	}
}

// awsView copies the group into its CloudWatch Logs-shaped form. The tag map
// is cloned so callers cannot mutate stored state.
func (g *logGroup) awsView() driver.AWSLogGroup {
	info := g.info
	info.Tags = maps.Clone(g.info.Tags)

	class := g.class
	if class == "" {
		class = driver.LogGroupClassStandard
	}

	out := driver.AWSLogGroup{LogGroupInfo: info, KmsKeyID: g.kmsKeyID, LogGroupClass: class}
	if g.dataProtection.Load() != nil {
		out.DataProtectionStatus = driver.DataProtectionStatusActivated
	}

	return out
}

// ListAWSLogGroups returns every log group matching filter, ordered by name.
// It backs both DescribeLogGroups (empty filter) and ListLogGroups.
func (m *Mock) ListAWSLogGroups(_ context.Context, filter driver.ListLogGroupsFilter) ([]driver.AWSLogGroup, error) {
	if filter.NamePattern != "" && !logGroupNamePatternRE.MatchString(filter.NamePattern) {
		return nil, errors.New(errors.InvalidArgument,
			"1 validation error detected: Value at 'logGroupNamePattern' failed to satisfy constraint: "+
				`Member must satisfy regular expression pattern: (\^?[\.\-_\/#A-Za-z0-9]{3,24})(\|\^?[\.\-_\/#A-Za-z0-9]{3,24}){0,4}`)
	}

	if filter.LogGroupClass != "" {
		if _, err := resolveLogGroupClass(filter.LogGroupClass); err != nil {
			return nil, err
		}
	}

	all := m.groups.SortedValues()
	out := make([]driver.AWSLogGroup, 0, len(all))

	for _, g := range all {
		v := g.awsView()
		if matchesLogGroupFilter(&v, filter) {
			out = append(out, v)
		}
	}

	return out, nil
}

func matchesLogGroupFilter(g *driver.AWSLogGroup, f driver.ListLogGroupsFilter) bool {
	if f.LogGroupClass != "" && g.LogGroupClass != f.LogGroupClass {
		return false
	}

	if f.NamePattern != "" && !matchesNamePattern(g.Name, f.NamePattern) {
		return false
	}

	for _, tf := range f.Tags {
		v, ok := g.Tags[tf.Key]
		if !ok || (len(tf.Values) > 0 && !slices.Contains(tf.Values, v)) {
			return false
		}
	}

	return true
}

// matchesNamePattern applies a logGroupNamePattern: any alternative matches,
// '^' anchors it to the start of the name, otherwise it is a case-sensitive
// substring.
func matchesNamePattern(name, pattern string) bool {
	for _, alt := range strings.Split(pattern, "|") {
		if prefix, anchored := strings.CutPrefix(alt, "^"); anchored {
			if strings.HasPrefix(name, prefix) {
				return true
			}

			continue
		}

		if strings.Contains(name, alt) {
			return true
		}
	}

	return false
}
