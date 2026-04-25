package chaos

import (
	"context"

	logdriver "github.com/stackshy/cloudemu/logging/driver"
)

// chaosLogging wraps a logging driver. Hot-path: log group CRUD plus
// PutLogEvents / GetLogEvents / FilterLogEvents. Streams and metric filters
// delegate through.
type chaosLogging struct {
	logdriver.Logging
	engine *Engine
}

// WrapLogging returns a logging driver that consults engine on log-group and
// event-ingest/query calls.
func WrapLogging(inner logdriver.Logging, engine *Engine) logdriver.Logging {
	return &chaosLogging{Logging: inner, engine: engine}
}

func (c *chaosLogging) CreateLogGroup(
	ctx context.Context, cfg logdriver.LogGroupConfig,
) (*logdriver.LogGroupInfo, error) {
	if err := applyChaos(ctx, c.engine, "logging", "CreateLogGroup"); err != nil {
		return nil, err
	}

	return c.Logging.CreateLogGroup(ctx, cfg)
}

func (c *chaosLogging) DeleteLogGroup(ctx context.Context, name string) error {
	if err := applyChaos(ctx, c.engine, "logging", "DeleteLogGroup"); err != nil {
		return err
	}

	return c.Logging.DeleteLogGroup(ctx, name)
}

func (c *chaosLogging) GetLogGroup(ctx context.Context, name string) (*logdriver.LogGroupInfo, error) {
	if err := applyChaos(ctx, c.engine, "logging", "GetLogGroup"); err != nil {
		return nil, err
	}

	return c.Logging.GetLogGroup(ctx, name)
}

func (c *chaosLogging) ListLogGroups(ctx context.Context) ([]logdriver.LogGroupInfo, error) {
	if err := applyChaos(ctx, c.engine, "logging", "ListLogGroups"); err != nil {
		return nil, err
	}

	return c.Logging.ListLogGroups(ctx)
}

func (c *chaosLogging) PutLogEvents(
	ctx context.Context, logGroup, streamName string, events []logdriver.LogEvent,
) error {
	if err := applyChaos(ctx, c.engine, "logging", "PutLogEvents"); err != nil {
		return err
	}

	return c.Logging.PutLogEvents(ctx, logGroup, streamName, events)
}

func (c *chaosLogging) GetLogEvents(
	ctx context.Context, input *logdriver.LogQueryInput,
) ([]logdriver.LogEvent, error) {
	if err := applyChaos(ctx, c.engine, "logging", "GetLogEvents"); err != nil {
		return nil, err
	}

	return c.Logging.GetLogEvents(ctx, input)
}

func (c *chaosLogging) FilterLogEvents(
	ctx context.Context, input *logdriver.FilterLogEventsInput,
) ([]logdriver.FilteredLogEvent, error) {
	if err := applyChaos(ctx, c.engine, "logging", "FilterLogEvents"); err != nil {
		return nil, err
	}

	return c.Logging.FilterLogEvents(ctx, input)
}
