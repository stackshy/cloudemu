package ecs

import (
	"context"
	"slices"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/services/ecs/driver"
)

const (
	defaultNamespaceListResults = 10
	maxNamespaceListResults     = 100
)

// Compile-time check that Mock implements the ServiceNamespaces capability.
var _ driver.ServiceNamespaces = (*Mock)(nil)

// ListServicesByNamespace lists the ARNs of the services whose Service Connect
// configuration names the namespace, across all clusters, sorted by ARN and
// paged with maxResults (1-100, default 10) and nextToken. A service is matched
// by the namespace string it was configured with (name or ARN); the emulator has
// no Cloud Map to resolve one to the other, so an unknown namespace lists
// nothing rather than failing.
func (m *Mock) ListServicesByNamespace(
	_ context.Context, namespace string, maxResults int, nextToken string,
) (serviceARNs []string, next string, err error) {
	if namespace == "" {
		return nil, "", apiErrf(errors.InvalidArgument, excInvalidParameter, "namespace is required.")
	}

	if maxResults < 0 || maxResults > maxNamespaceListResults {
		return nil, "", apiErrf(errors.InvalidArgument, excInvalidParameter,
			"maxResults must be between 1 and %d.", maxNamespaceListResults)
	}

	var arns []string

	for _, svc := range m.services.All() {
		if svc.Status != statusInactive && svc.ServiceConnect != nil && svc.ServiceConnect.Namespace == namespace {
			arns = append(arns, svc.ARN)
		}
	}

	slices.SortFunc(arns, strings.Compare)

	size := maxResults
	if size == 0 {
		size = defaultNamespaceListResults
	}

	page, perr := pagination.Paginate(arns, nextToken, size)
	if perr != nil {
		return nil, "", apiErrf(errors.InvalidArgument, excInvalidParameter, "invalid nextToken: %v", perr)
	}

	return page.Items, page.NextPageToken, nil
}
