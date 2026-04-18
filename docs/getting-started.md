# Getting Started

## Installation

```bash
go get github.com/stackshy/cloudemu
```

Requires Go 1.25.0 or later.

## Creating Providers

CloudEmu provides three top-level factory functions, one per cloud provider. Each returns a provider struct with all 16 services ready to use.

### AWS

```go
package main

import (
    "context"

    "github.com/stackshy/cloudemu"
)

func main() {
    ctx := context.Background()
    aws := cloudemu.NewAWS()

    // All 16 services are available:
    // aws.S3, aws.EC2, aws.DynamoDB, aws.Lambda, aws.VPC,
    // aws.CloudWatch, aws.IAM, aws.Route53, aws.ELB, aws.SQS,
    // aws.ElastiCache, aws.SecretsManager, aws.CloudWatchLogs,
    // aws.SNS, aws.ECR, aws.EventBridge
    _ = ctx
}
```

### Azure

```go
azure := cloudemu.NewAzure()

// azure.BlobStorage, azure.VirtualMachines, azure.CosmosDB,
// azure.Functions, azure.VNet, azure.Monitor, azure.IAM,
// azure.DNS, azure.LB, azure.ServiceBus, azure.Cache,
// azure.KeyVault, azure.LogAnalytics, azure.NotificationHubs,
// azure.ACR, azure.EventGrid
```

### GCP

```go
gcp := cloudemu.NewGCP()

// gcp.GCS, gcp.GCE, gcp.Firestore, gcp.CloudFunctions,
// gcp.VPC, gcp.CloudMonitoring, gcp.IAM, gcp.CloudDNS,
// gcp.LB, gcp.PubSub, gcp.Memorystore, gcp.SecretManager,
// gcp.CloudLogging, gcp.FCM, gcp.ArtifactRegistry, gcp.Eventarc
```

## Basic Storage Example

Create a bucket, store an object, and retrieve it.

```go
package main

import (
    "context"
    "fmt"

    "github.com/stackshy/cloudemu"
)

func main() {
    ctx := context.Background()
    aws := cloudemu.NewAWS()

    // Create a bucket
    err := aws.S3.CreateBucket(ctx, "my-bucket")
    if err != nil {
        panic(err)
    }

    // Store an object
    data := []byte("Hello, CloudEmu!")
    err = aws.S3.PutObject(ctx, "my-bucket", "greeting.txt", data, "text/plain", nil)
    if err != nil {
        panic(err)
    }

    // Retrieve it
    obj, err := aws.S3.GetObject(ctx, "my-bucket", "greeting.txt")
    if err != nil {
        panic(err)
    }

    fmt.Printf("Content: %s\n", obj.Data)
    fmt.Printf("Size: %d bytes\n", obj.Info.Size)
    fmt.Printf("Content-Type: %s\n", obj.Info.ContentType)
}
```

## Basic Compute Example

Launch an instance, describe it, and terminate it.

```go
package main

import (
    "context"
    "fmt"

    "github.com/stackshy/cloudemu"
    cdriver "github.com/stackshy/cloudemu/compute/driver"
)

func main() {
    ctx := context.Background()
    aws := cloudemu.NewAWS()

    // Launch an instance
    instances, err := aws.EC2.RunInstances(ctx, cdriver.InstanceConfig{
        ImageID:      "ami-12345678",
        InstanceType: "t2.micro",
        Tags: map[string]string{
            "Name": "my-server",
            "Env":  "test",
        },
    }, 1)
    if err != nil {
        panic(err)
    }

    instanceID := instances[0].ID
    fmt.Printf("Launched: %s (state: %s)\n", instanceID, instances[0].State)

    // Describe instances
    described, err := aws.EC2.DescribeInstances(ctx, []string{instanceID}, nil)
    if err != nil {
        panic(err)
    }
    fmt.Printf("State: %s, IP: %s\n", described[0].State, described[0].PrivateIP)

    // Stop, then terminate
    _ = aws.EC2.StopInstances(ctx, []string{instanceID})
    _ = aws.EC2.TerminateInstances(ctx, []string{instanceID})
}
```

## Basic Database Example

Create a table, put an item, and get it back.

```go
package main

import (
    "context"
    "fmt"

    "github.com/stackshy/cloudemu"
    ddriver "github.com/stackshy/cloudemu/database/driver"
)

func main() {
    ctx := context.Background()
    aws := cloudemu.NewAWS()

    // Create a table with partition key and sort key
    err := aws.DynamoDB.CreateTable(ctx, ddriver.TableConfig{
        Name:         "users",
        PartitionKey: "userID",
        SortKey:      "email",
    })
    if err != nil {
        panic(err)
    }

    // Put an item
    err = aws.DynamoDB.PutItem(ctx, "users", map[string]any{
        "userID": "u-001",
        "email":  "alice@example.com",
        "name":   "Alice",
        "age":    30,
    })
    if err != nil {
        panic(err)
    }

    // Get the item
    item, err := aws.DynamoDB.GetItem(ctx, "users", map[string]any{
        "userID": "u-001",
        "email":  "alice@example.com",
    })
    if err != nil {
        panic(err)
    }

    fmt.Printf("Name: %s, Age: %v\n", item["name"], item["age"])

    // Query by partition key with sort key condition
    result, err := aws.DynamoDB.Query(ctx, ddriver.QueryInput{
        Table: "users",
        KeyCondition: ddriver.KeyCondition{
            PartitionKey: "userID",
            PartitionVal: "u-001",
            SortOp:       "BEGINS_WITH",
            SortVal:      "alice",
        },
    })
    if err != nil {
        panic(err)
    }

    fmt.Printf("Found %d items\n", result.Count)
}
```

## Configuration Options

All three factory functions accept `config.Option` values for customization.

```go
import (
    "time"

    "github.com/stackshy/cloudemu"
    "github.com/stackshy/cloudemu/config"
)

// Custom region
aws := cloudemu.NewAWS(config.WithRegion("eu-west-1"))

// Custom account ID
aws = cloudemu.NewAWS(config.WithAccountID("987654321098"))

// Custom project ID (primarily for GCP)
gcp := cloudemu.NewGCP(config.WithProjectID("my-gcp-project"))

// Simulated latency on all operations
aws = cloudemu.NewAWS(config.WithLatency(10 * time.Millisecond))

// Deterministic time for testing
clock := config.NewFakeClock(time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC))
aws = cloudemu.NewAWS(config.WithClock(clock))

// Combine multiple options
aws = cloudemu.NewAWS(
    config.WithRegion("us-west-2"),
    config.WithAccountID("111222333444"),
    config.WithClock(clock),
)
```

### Available Options

| Option | Default | Description |
|--------|---------|-------------|
| `WithClock(clock)` | `RealClock{}` | Clock implementation for timestamps |
| `WithRegion(region)` | `"us-east-1"` | Cloud region string |
| `WithAccountID(id)` | `"123456789012"` | AWS account ID / Azure subscription ID |
| `WithProjectID(id)` | `"mock-project"` | GCP project ID |
| `WithLatency(d)` | `0` | Simulated latency added to all operations |

## Error Handling

CloudEmu uses canonical error codes from the `errors` package. Use the helper functions to check error types.

```go
import (
    cerrors "github.com/stackshy/cloudemu/errors"
)

// Try to get a non-existent bucket
_, err := aws.S3.GetObject(ctx, "nonexistent", "key")
if cerrors.IsNotFound(err) {
    fmt.Println("Object not found")
}

// Try to create a duplicate
err = aws.S3.CreateBucket(ctx, "my-bucket")
err = aws.S3.CreateBucket(ctx, "my-bucket")
if cerrors.IsAlreadyExists(err) {
    fmt.Println("Bucket already exists")
}

// Extract the error code
code := cerrors.GetCode(err)
switch code {
case cerrors.NotFound:
    // handle not found
case cerrors.AlreadyExists:
    // handle duplicate
case cerrors.InvalidArgument:
    // handle bad input
case cerrors.FailedPrecondition:
    // handle illegal state transition
case cerrors.PermissionDenied:
    // handle access denied
case cerrors.Throttled:
    // handle rate limit
default:
    // handle other errors
}
```

## Using the Topology Engine

The topology engine evaluates network reachability using the live state of compute, networking, and DNS services.

```go
aws := cloudemu.NewAWS()
ctx := context.Background()

// Set up network infrastructure
vpc, _ := aws.VPC.CreateVPC(ctx, ndriver.VPCConfig{CIDRBlock: "10.0.0.0/16"})
subnet, _ := aws.VPC.CreateSubnet(ctx, ndriver.SubnetConfig{
    VPCID:     vpc.ID,
    CIDRBlock: "10.0.1.0/24",
})
sg, _ := aws.VPC.CreateSecurityGroup(ctx, ndriver.SecurityGroupConfig{
    Name:  "web-sg",
    VPCID: vpc.ID,
})

// Allow inbound HTTP
aws.VPC.AddIngressRule(ctx, sg.ID, ndriver.SecurityRule{
    Protocol: "tcp",
    FromPort: 80,
    ToPort:   80,
    CIDR:     "10.0.0.0/16",
})

// Launch instances
instances, _ := aws.EC2.RunInstances(ctx, cdriver.InstanceConfig{
    ImageID:        "ami-12345678",
    InstanceType:   "t2.micro",
    SubnetID:       subnet.ID,
    SecurityGroups: []string{sg.ID},
}, 2)

// Now use the topology engine to check connectivity
// between instances, evaluate security groups, etc.
```

## Running Tests

```bash
# Compile all packages
go build ./...

# Run static analysis
go vet ./...

# Run all tests
go test -v ./...

# Run a specific test
go test -v -run TestAWSS3Operations ./...

# Run tests with coverage
go test -covermode=atomic -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

### Writing Tests with CloudEmu

```go
package myapp_test

import (
    "context"
    "testing"
    "time"

    "github.com/stackshy/cloudemu"
    "github.com/stackshy/cloudemu/config"
    sdriver "github.com/stackshy/cloudemu/storage/driver"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestStorageWorkflow(t *testing.T) {
    clock := config.NewFakeClock(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
    aws := cloudemu.NewAWS(config.WithClock(clock))
    ctx := context.Background()

    // Create bucket
    err := aws.S3.CreateBucket(ctx, "test-bucket")
    require.NoError(t, err)

    // Put object
    err = aws.S3.PutObject(ctx, "test-bucket", "file.txt", []byte("data"), "text/plain", nil)
    require.NoError(t, err)

    // Verify object
    obj, err := aws.S3.GetObject(ctx, "test-bucket", "file.txt")
    require.NoError(t, err)
    assert.Equal(t, []byte("data"), obj.Data)
    assert.Equal(t, "text/plain", obj.Info.ContentType)

    // List objects
    result, err := aws.S3.ListObjects(ctx, "test-bucket", sdriver.ListOptions{})
    require.NoError(t, err)
    assert.Len(t, result.Objects, 1)
}
```
