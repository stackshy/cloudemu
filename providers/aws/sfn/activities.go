package sfn

import (
	"context"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/regionctx"
	"github.com/stackshy/cloudemu/v2/services/sfn/driver"
)

func (m *Mock) getActivity(arn string) (*actData, error) {
	if !validActivityARN(arn) {
		return nil, invalidArn("%q is not a valid activity ARN", arn)
	}

	ad, ok := m.activities.Get(arn)
	if !ok {
		return nil, activityNotFound(arn)
	}

	return ad, nil
}

func (m *Mock) CreateActivity(
	ctx context.Context, name string, tags map[string]string,
) (arn string, created time.Time, err error) {
	if name == "" {
		return "", time.Time{}, invalidName("activity name is required")
	}

	arn = m.activityARN(regionctx.RegionOr(ctx, m.opts.Region), name)
	now := m.now()

	if !m.activities.SetIfAbsent(arn, &actData{act: driver.Activity{
		ARN: arn, Name: name, CreationDate: now, Tags: copyTags(tags),
	}}) {
		return "", time.Time{}, activityAlreadyExists(name)
	}

	return arn, now, nil
}

func (m *Mock) DescribeActivity(_ context.Context, arn string) (*driver.Activity, error) {
	ad, err := m.getActivity(arn)
	if err != nil {
		return nil, err
	}

	ad.mu.RLock()
	defer ad.mu.RUnlock()

	out := ad.act
	out.Tags = copyTags(ad.act.Tags)

	return &out, nil
}

func (m *Mock) DeleteActivity(_ context.Context, arn string) error {
	if !validActivityARN(arn) {
		return invalidArn("%q is not a valid activity ARN", arn)
	}

	m.activities.Delete(arn)

	return nil
}

func (m *Mock) ListActivities(_ context.Context) ([]driver.Activity, error) {
	all := m.activities.SortedValues()
	out := make([]driver.Activity, 0, len(all))

	for _, ad := range all {
		ad.mu.RLock()
		act := ad.act
		act.Tags = copyTags(ad.act.Tags)
		ad.mu.RUnlock()

		out = append(out, act)
	}

	return out, nil
}

// GetActivityTask returns no task: the emulator has no real ASL interpreter, so
// no execution ever schedules an activity task. Real SFN long-polls and returns
// an empty task token when none is available, which is what callers observe.
func (m *Mock) GetActivityTask(_ context.Context, activityArn, _ string) (taskToken, input string, err error) {
	if _, err := m.getActivity(activityArn); err != nil {
		return "", "", err
	}

	return "", "", nil
}

// SendTaskSuccess accepts a task token. Because the emulator never schedules
// activity tasks, any non-empty token that was not issued is InvalidToken.
func (m *Mock) SendTaskSuccess(_ context.Context, taskToken, _ string) error {
	return m.consumeTask(taskToken)
}

func (m *Mock) SendTaskFailure(_ context.Context, taskToken, _, _ string) error {
	return m.consumeTask(taskToken)
}

func (m *Mock) SendTaskHeartbeat(_ context.Context, taskToken string) error {
	return m.consumeTask(taskToken)
}

func (m *Mock) consumeTask(taskToken string) error {
	if taskToken == "" {
		return invalidToken("task token is required")
	}

	m.tasksMu.RLock()
	_, ok := m.tasks[taskToken]
	m.tasksMu.RUnlock()

	if !ok {
		return invalidToken("task token %q is not valid", taskToken)
	}

	return nil
}
