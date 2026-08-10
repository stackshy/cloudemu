package cloudtrail

import (
	"context"
	"net"
	"regexp"
	"sort"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

// trailNameRe matches CloudTrail's trail-name rule: ASCII letters/digits,
// periods, underscores, dashes; start and end with a letter or digit.
var trailNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*[A-Za-z0-9]$`)

// validTrailName enforces CloudTrail's documented trail-name rules (3-128
// chars, allowed charset, no adjacent separators, not an IP address).
func validTrailName(name string) error {
	if len(name) < minTrailNameLen || len(name) > maxTrailNameLen {
		return errInvalidTrailName("trail name %q must be between %d and %d characters",
			name, minTrailNameLen, maxTrailNameLen)
	}

	if !trailNameRe.MatchString(name) {
		return errInvalidTrailName("trail name %q must start and end with a letter or number "+
			"and contain only letters, numbers, periods, underscores, or dashes", name)
	}

	for _, pair := range []string{"..", "__", "--", "._", "_.", ".-", "-.", "_-", "-_"} {
		if hasAdjacent(name, pair) {
			return errInvalidTrailName("trail name %q must not contain adjacent separators", name)
		}
	}

	if net.ParseIP(name) != nil {
		return errInvalidTrailName("trail name %q must not be in IP address format", name)
	}

	return nil
}

func hasAdjacent(name, pair string) bool {
	for i := 0; i+1 < len(name); i++ {
		if name[i] == pair[0] && name[i+1] == pair[1] {
			return true
		}
	}

	return false
}

// CreateTrail stores a trail configuration and returns its ARN. The name/ARN is
// claimed atomically so a concurrent duplicate is a TrailAlreadyExistsException.
//
//nolint:gocritic // in is the public CreateTrail input, taken by value to match the driver API
func (m *Mock) CreateTrail(_ context.Context, in driver.CreateTrailInput) (*driver.Trail, error) {
	if err := validTrailName(in.Name); err != nil {
		return nil, err
	}

	if in.S3BucketName == "" {
		return nil, errInvalidParameter("S3BucketName is required")
	}

	globalEvents := true
	if in.IncludeGlobalServiceEvents != nil {
		globalEvents = *in.IncludeGlobalServiceEvents
	}

	trail := driver.Trail{
		Name:                       in.Name,
		TrailARN:                   m.trailARN(in.Name),
		S3BucketName:               in.S3BucketName,
		S3KeyPrefix:                in.S3KeyPrefix,
		SNSTopicName:               in.SNSTopicName,
		IncludeGlobalServiceEvents: globalEvents,
		IsMultiRegionTrail:         in.IsMultiRegionTrail,
		IsOrganizationTrail:        in.IsOrganizationTrail,
		HomeRegion:                 m.opts.Region,
		LogFileValidationEnabled:   in.LogFileValidationEnabled,
		CloudWatchLogsLogGroupARN:  in.CloudWatchLogsLogGroupARN,
		CloudWatchLogsRoleARN:      in.CloudWatchLogsRoleARN,
		KMSKeyID:                   in.KMSKeyID,
		CreatedAt:                  m.now(),
	}
	if in.SNSTopicName != "" {
		trail.SNSTopicARN = idgen.AWSARN("sns", m.opts.Region, m.opts.AccountID, in.SNSTopicName)
	}

	td := &trailData{trail: trail, status: driver.TrailStatus{IsLogging: false}}

	if !m.trails.SetIfAbsent(in.Name, td) {
		return nil, errTrailExists(in.Name)
	}

	m.trailARNIdx.Set(trail.TrailARN, in.Name)
	m.storeResourceTags(trail.TrailARN, in.Tags)

	out := td.trail

	return &out, nil
}

// GetTrail returns a trail's configuration.
func (m *Mock) GetTrail(_ context.Context, nameOrARN string) (*driver.Trail, error) {
	td, err := m.resolveTrail(nameOrARN)
	if err != nil {
		return nil, err
	}

	td.mu.RLock()
	defer td.mu.RUnlock()

	out := td.trail

	return &out, nil
}

// UpdateTrail applies the non-nil fields of in and returns the updated trail.
//
//nolint:gocyclo,gocritic // one branch per optional field; in matches the driver signature (by value).
func (m *Mock) UpdateTrail(_ context.Context, in driver.UpdateTrailInput) (*driver.Trail, error) {
	td, err := m.resolveTrail(in.Name)
	if err != nil {
		return nil, err
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	t := &td.trail
	if in.S3BucketName != nil {
		t.S3BucketName = *in.S3BucketName
	}

	if in.S3KeyPrefix != nil {
		t.S3KeyPrefix = *in.S3KeyPrefix
	}

	if in.SNSTopicName != nil {
		t.SNSTopicName = *in.SNSTopicName
		if *in.SNSTopicName != "" {
			t.SNSTopicARN = idgen.AWSARN("sns", m.opts.Region, m.opts.AccountID, *in.SNSTopicName)
		} else {
			t.SNSTopicARN = ""
		}
	}

	if in.IncludeGlobalServiceEvents != nil {
		t.IncludeGlobalServiceEvents = *in.IncludeGlobalServiceEvents
	}

	if in.IsMultiRegionTrail != nil {
		t.IsMultiRegionTrail = *in.IsMultiRegionTrail
	}

	if in.IsOrganizationTrail != nil {
		t.IsOrganizationTrail = *in.IsOrganizationTrail
	}

	if in.LogFileValidationEnabled != nil {
		t.LogFileValidationEnabled = *in.LogFileValidationEnabled
	}

	if in.CloudWatchLogsLogGroupARN != nil {
		t.CloudWatchLogsLogGroupARN = *in.CloudWatchLogsLogGroupARN
	}

	if in.CloudWatchLogsRoleARN != nil {
		t.CloudWatchLogsRoleARN = *in.CloudWatchLogsRoleARN
	}

	if in.KMSKeyID != nil {
		t.KMSKeyID = *in.KMSKeyID
	}

	out := *t

	return &out, nil
}

// DeleteTrail removes a trail, its ARN index entry, and its tags.
func (m *Mock) DeleteTrail(_ context.Context, nameOrARN string) error {
	td, err := m.resolveTrail(nameOrARN)
	if err != nil {
		return err
	}

	td.mu.Lock()
	name := td.trail.Name
	arn := td.trail.TrailARN
	td.mu.Unlock()

	m.trails.Delete(name)
	m.trailARNIdx.Delete(arn)
	m.deleteResourceTags(arn)

	return nil
}

// DescribeTrails returns the named trails, or all trails when nameList is empty.
func (m *Mock) DescribeTrails(_ context.Context, nameList []string) ([]driver.Trail, error) {
	if len(nameList) > 0 {
		out := make([]driver.Trail, 0, len(nameList))

		for _, n := range nameList {
			td, err := m.resolveTrail(n)
			if err != nil {
				continue // describe silently omits unknown names, matching AWS
			}

			td.mu.RLock()
			out = append(out, td.trail)
			td.mu.RUnlock()
		}

		return out, nil
	}

	all := m.trails.All()
	out := make([]driver.Trail, 0, len(all))

	for _, td := range all {
		td.mu.RLock()
		out = append(out, td.trail)
		td.mu.RUnlock()
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

// ListTrails returns all trails ordered by name, paginated after nextToken.
func (m *Mock) ListTrails(_ context.Context, nextToken string) ([]driver.Trail, string, error) {
	all := m.trails.All()

	names := make([]string, 0, len(all))
	for n := range all {
		names = append(names, n)
	}

	sort.Strings(names)

	out := make([]driver.Trail, 0, len(names))
	started := nextToken == ""

	for _, n := range names {
		if !started {
			if n == nextToken {
				started = true
			}

			continue
		}

		if len(out) == defaultMaxResults {
			return out, out[len(out)-1].Name, nil
		}

		td := all[n]
		td.mu.RLock()
		out = append(out, td.trail)
		td.mu.RUnlock()
	}

	return out, "", nil
}

// GetTrailStatus returns a trail's logging status.
func (m *Mock) GetTrailStatus(_ context.Context, nameOrARN string) (*driver.TrailStatus, error) {
	td, err := m.resolveTrail(nameOrARN)
	if err != nil {
		return nil, err
	}

	td.mu.RLock()
	defer td.mu.RUnlock()

	out := td.status

	return &out, nil
}

// StartLogging flips a trail's IsLogging to true, recording the start time.
func (m *Mock) StartLogging(_ context.Context, nameOrARN string) error {
	return m.setLogging(nameOrARN, true)
}

// StopLogging flips a trail's IsLogging to false, recording the stop time.
func (m *Mock) StopLogging(_ context.Context, nameOrARN string) error {
	return m.setLogging(nameOrARN, false)
}

func (m *Mock) setLogging(nameOrARN string, on bool) error {
	td, err := m.resolveTrail(nameOrARN)
	if err != nil {
		return err
	}

	td.mu.Lock()
	defer td.mu.Unlock()

	td.status.IsLogging = on
	if on {
		td.status.StartLoggingTime = m.now()
		td.status.LatestDeliveryTime = m.now()
	} else {
		td.status.StopLoggingTime = m.now()
	}

	return nil
}
