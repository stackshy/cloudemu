package vnet

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/networking/driver"
)

// Compile-time check that Mock serves the Azure application-security-group surface.
var _ driver.AzureApplicationSecurityGroups = (*Mock)(nil)

// asgKey composes the store key from the ARM addressing pair. Resource-group
// names are case-insensitive in Azure, so it is lower-cased; the ASG name is
// preserved as-is, mirroring nicKey.
func asgKey(resourceGroup, name string) string {
	return strings.ToLower(resourceGroup) + "/" + name
}

// PutAzureApplicationSecurityGroup creates or replaces an ASG in place, keyed by
// (resourceGroup, name), so a repeat createOrUpdate PUT updates rather than
// duplicating.
func (m *Mock) PutAzureApplicationSecurityGroup(
	_ context.Context, asg driver.AzureApplicationSecurityGroup,
) driver.AzureApplicationSecurityGroup {
	stored := cloneASG(asg)
	m.azureASGs.Set(asgKey(asg.ResourceGroup, asg.Name), stored)

	return cloneASG(stored)
}

// GetAzureApplicationSecurityGroup returns the ASG identified by (resourceGroup, name).
func (m *Mock) GetAzureApplicationSecurityGroup(
	_ context.Context, resourceGroup, name string,
) (driver.AzureApplicationSecurityGroup, bool) {
	asg, ok := m.azureASGs.Get(asgKey(resourceGroup, name))
	if !ok {
		return driver.AzureApplicationSecurityGroup{}, false
	}

	return cloneASG(asg), true
}

// DeleteAzureApplicationSecurityGroup removes the ASG, reporting whether it existed.
func (m *Mock) DeleteAzureApplicationSecurityGroup(_ context.Context, resourceGroup, name string) bool {
	return m.azureASGs.Delete(asgKey(resourceGroup, name))
}

// ListAzureApplicationSecurityGroups returns the ASGs in a resource group, or all
// when resourceGroup is empty (subscription-wide list), ordered by key.
func (m *Mock) ListAzureApplicationSecurityGroups(
	_ context.Context, resourceGroup string,
) []driver.AzureApplicationSecurityGroup {
	out := make([]driver.AzureApplicationSecurityGroup, 0)

	for _, asg := range m.azureASGs.SortedValues() {
		if resourceGroup != "" && !strings.EqualFold(asg.ResourceGroup, resourceGroup) {
			continue
		}

		out = append(out, cloneASG(asg))
	}

	return out
}

// cloneASG deep-copies the tag map so stored and returned values never alias a
// caller's map.
func cloneASG(asg driver.AzureApplicationSecurityGroup) driver.AzureApplicationSecurityGroup {
	out := driver.AzureApplicationSecurityGroup{
		Name:          asg.Name,
		ResourceGroup: asg.ResourceGroup,
		Location:      asg.Location,
	}
	if len(asg.Tags) > 0 {
		out.Tags = copyTags(asg.Tags)
	}

	return out
}
