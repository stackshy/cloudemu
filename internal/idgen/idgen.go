// Package idgen provides ID generators for various cloud resource types.
package idgen

import (
	"fmt"
	"sync/atomic"
)

var counter uint64

// next returns a monotonically increasing number.
func next() uint64 {
	return atomic.AddUint64(&counter, 1)
}

// GenerateID generates an ID with the given prefix (e.g., "i-", "vpc-", "sg-").
func GenerateID(prefix string) string {
	return fmt.Sprintf("%s%08x", prefix, next())
}

// ARN generates an AWS ARN.
func ARN(partition, service, region, accountID, resource string) string {
	return fmt.Sprintf("arn:%s:%s:%s:%s:%s", partition, service, region, accountID, resource)
}

// AWSARN generates an AWS ARN with the standard "aws" partition.
func AWSARN(service, region, accountID, resource string) string {
	return ARN("aws", service, region, accountID, resource)
}

// AzureID generates an Azure resource ID.
func AzureID(subscriptionID, resourceGroup, provider, resourceType, name string) string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/%s/%s/%s",
		subscriptionID, resourceGroup, provider, resourceType, name)
}

// GCPID generates a GCP resource self-link.
func GCPID(project, resourceType, name string) string {
	return fmt.Sprintf("projects/%s/%s/%s", project, resourceType, name)
}

// Reset resets the counter (for testing).
func Reset() {
	atomic.StoreUint64(&counter, 0)
}
