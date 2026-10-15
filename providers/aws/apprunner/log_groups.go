package apprunner

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/apprunner/driver"
	logdriver "github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// logGroupNames are the service and application log groups App Runner writes a
// service's logs to.
func logGroupNames(svc *driver.Service) []string {
	base := "/aws/apprunner/" + svc.ServiceName + "/" + svc.ServiceID

	return []string{base + "/service", base + "/application"}
}

// createLogGroups creates a service's log groups when CloudWatch Logs is wired.
func (m *Mock) createLogGroups(ctx context.Context, svc *driver.Service) {
	if m.logs == nil {
		return
	}

	for _, name := range logGroupNames(svc) {
		_, _ = m.logs.CreateLogGroup(ctx, logdriver.LogGroupConfig{Name: name})
	}
}
