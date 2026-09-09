package eventgrid

import (
	"context"

	ebdriver "github.com/stackshy/cloudemu/v2/services/eventbus/driver"
)

// eventBusTagWriter is the optional tag-write capability of an event bus
// backend (the Azure eventgrid.Mock provides it) that lets updateTopic set a
// topic's tags to an explicit value, including empty. It exists because
// EventBusConfig.Tags cannot carry the distinction Topics.Update needs: a nil
// map means both "the request omitted tags" and "the request supplied an
// empty tags object," and UpdateEventBus's cfg.Tags != nil gate collapses
// both to "leave tags unchanged" — silently no-oping an explicit wipe to
// empty. Routing the wipe through this capability instead keeps the fix
// local to Azure Event Grid without changing the shared driver contract used
// by AWS/GCP event-bus backends.
type eventBusTagWriter interface {
	SetEventBusTags(ctx context.Context, name string, tags map[string]string) (*ebdriver.EventBusInfo, error)
}
