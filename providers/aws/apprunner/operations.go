package apprunner

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
)

// ListOperations returns the per-service operations recorded by the service
// mutations (Create/Update/Pause/Resume/Delete/StartDeployment), paginated by
// operation id. The service must exist.
func (m *Mock) ListOperations(
	_ context.Context, serviceArn, nextToken string, maxResults int32,
) ([]driver.OperationSummary, string, error) {
	sd, err := m.getService(serviceArn)
	if err != nil {
		return nil, "", err
	}

	sd.mu.RLock()
	out := make([]driver.OperationSummary, len(sd.ops))
	copy(out, sd.ops)
	sd.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return paginate(out, nextToken, maxResults, func(o driver.OperationSummary) string { return o.ID })
}
