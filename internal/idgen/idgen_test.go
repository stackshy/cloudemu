package idgen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateID(t *testing.T) {
	Reset()

	tests := []struct {
		name         string
		prefix       string
		expectPrefix string
	}{
		{name: "instance prefix", prefix: "i-", expectPrefix: "i-"},
		{name: "vpc prefix", prefix: "vpc-", expectPrefix: "vpc-"},
		{name: "empty prefix", prefix: "", expectPrefix: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id := GenerateID(tc.prefix)
			assert.True(t, strings.HasPrefix(id, tc.expectPrefix))
			assert.Greater(t, len(id), len(tc.prefix))
		})
	}
}

func TestGenerateID_Uniqueness(t *testing.T) {
	Reset()

	seen := make(map[string]bool)

	for i := 0; i < 100; i++ {
		id := GenerateID("test-")
		require.False(t, seen[id], "duplicate ID generated: %s", id)
		seen[id] = true
	}
}

func TestARN(t *testing.T) {
	tests := []struct {
		name      string
		partition string
		service   string
		region    string
		accountID string
		resource  string
		expected  string
	}{
		{
			name:      "s3 bucket",
			partition: "aws",
			service:   "s3",
			region:    "",
			accountID: "",
			resource:  "my-bucket",
			expected:  "arn:aws:s3:::my-bucket",
		},
		{
			name:      "ec2 instance",
			partition: "aws",
			service:   "ec2",
			region:    "us-east-1",
			accountID: "123456789012",
			resource:  "instance/i-1234",
			expected:  "arn:aws:ec2:us-east-1:123456789012:instance/i-1234",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ARN(tc.partition, tc.service, tc.region, tc.accountID, tc.resource)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestAWSARN(t *testing.T) {
	tests := []struct {
		name      string
		service   string
		region    string
		accountID string
		resource  string
		expected  string
	}{
		{
			name:      "lambda function",
			service:   "lambda",
			region:    "us-west-2",
			accountID: "111111111111",
			resource:  "function:my-func",
			expected:  "arn:aws:lambda:us-west-2:111111111111:function:my-func",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := AWSARN(tc.service, tc.region, tc.accountID, tc.resource)
			assert.Equal(t, tc.expected, result)
			assert.True(t, strings.HasPrefix(result, "arn:aws:"))
		})
	}
}

func TestAzureID(t *testing.T) {
	tests := []struct {
		name           string
		subscriptionID string
		resourceGroup  string
		provider       string
		resourceType   string
		resourceName   string
		expected       string
	}{
		{
			name:           "virtual machine",
			subscriptionID: "sub-123",
			resourceGroup:  "rg-test",
			provider:       "Microsoft.Compute",
			resourceType:   "virtualMachines",
			resourceName:   "my-vm",
			expected:       "/subscriptions/sub-123/resourceGroups/rg-test/providers/Microsoft.Compute/virtualMachines/my-vm",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := AzureID(tc.subscriptionID, tc.resourceGroup, tc.provider, tc.resourceType, tc.resourceName)
			assert.Equal(t, tc.expected, result)
			assert.True(t, strings.HasPrefix(result, "/subscriptions/"))
		})
	}
}

func TestGCPID(t *testing.T) {
	tests := []struct {
		name         string
		project      string
		resourceType string
		resourceName string
		expected     string
	}{
		{
			name:         "compute instance",
			project:      "my-project",
			resourceType: "zones/us-central1-a/instances",
			resourceName: "my-vm",
			expected:     "projects/my-project/zones/us-central1-a/instances/my-vm",
		},
		{
			name:         "gcs bucket",
			project:      "proj-1",
			resourceType: "buckets",
			resourceName: "my-bucket",
			expected:     "projects/proj-1/buckets/my-bucket",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := GCPID(tc.project, tc.resourceType, tc.resourceName)
			assert.Equal(t, tc.expected, result)
			assert.True(t, strings.HasPrefix(result, "projects/"))
		})
	}
}

func TestReset(t *testing.T) {
	Reset()
	id1 := GenerateID("r-")

	Reset()
	id2 := GenerateID("r-")

	assert.Equal(t, id1, id2)
}
