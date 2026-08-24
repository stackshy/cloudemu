// Package idgen provides ID generators for various cloud resource types.
package idgen

import (
	"fmt"
	"hash/fnv"
	"sync/atomic"
)

// guidNodeMask isolates the low 48 bits used as a GUID's node field.
const guidNodeMask = 0xffffffffffff

// SyntheticGUID derives a deterministic GUID-shaped string from seed. The value
// is synthetic (a stand-in for an Azure principal/tenant id), not a real
// security identifier — the same seed always yields the same GUID so tests are
// stable.
func SyntheticGUID(seed string) string {
	h1 := fnv.New64a()
	_, _ = h1.Write([]byte(seed))
	a := h1.Sum64()

	h2 := fnv.New64a()
	_, _ = h2.Write([]byte(seed + "#salt"))
	b := h2.Sum64()

	//nolint:gosec,mnd // intentional narrowing + GUID field-width shifts
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(a>>32), uint16(a>>16), uint16(a), uint16(b>>48), b&guidNodeMask)
}

//nolint:gochecknoglobals // counter must be a package-level variable for atomic operations across all ID generators
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
