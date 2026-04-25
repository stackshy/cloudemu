# SDK-Compatible HTTP Server

CloudEmu includes an HTTP server that speaks the real AWS wire protocols. Point the actual `aws-sdk-go-v2` clients at it (via `BaseEndpoint`) and your production code runs unchanged against the in-memory backend.

Nothing to mock. No Docker. No accounts. The same SDK calls you'd run against real AWS hit a local `httptest.NewServer` and get back SDK-decodable responses.

## Why

CloudEmu's Go API is great for new code you write for testing. But most real apps already use `aws-sdk-go-v2` directly. Rewriting those call sites just to test against an emulator is friction. The SDK server removes that friction — change the endpoint, done.

## Quick start

```go
import (
    "net/http/httptest"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/service/s3"
    "github.com/stackshy/cloudemu"
    awsserver "github.com/stackshy/cloudemu/server/aws"
)

cloud := cloudemu.NewAWS()
srv := awsserver.New(awsserver.Drivers{
    S3:       cloud.S3,
    DynamoDB: cloud.DynamoDB,
    EC2:      cloud.EC2,
})
ts := httptest.NewServer(srv)
defer ts.Close()

client := s3.NewFromConfig(cfg, func(o *s3.Options) {
    o.BaseEndpoint = aws.String(ts.URL)
    o.UsePathStyle = true
})

// Use the real SDK exactly as you would against AWS.
client.PutObject(ctx, &s3.PutObjectInput{...})
```

Region and credentials can be any dummy values — the server doesn't validate signatures.

## Currently supported

| Service | Operations |
|---------|-----------|
| **S3** | CreateBucket, DeleteBucket, ListBuckets, PutObject, GetObject, HeadObject, DeleteObject, ListObjectsV2 (with prefix, delimiter, common prefixes, continuation token), CopyObject |
| **DynamoDB** | CreateTable, DeleteTable, DescribeTable, ListTables, PutItem, GetItem, DeleteItem, UpdateItem (SET/REMOVE), Query, Scan (with FilterExpression), BatchWriteItem, BatchGetItem, TransactWriteItems |
| **EC2** | RunInstances (tags, security groups, multi-count), DescribeInstances (filters: `instance-id`, `instance-type`, `instance-state-name`, `tag:*`), StartInstances, StopInstances, RebootInstances, TerminateInstances, ModifyInstanceAttribute |
| **EC2 — VPC** | CreateVpc, DeleteVpc, DescribeVpcs |
| **EC2 — Subnet** | CreateSubnet, DeleteSubnet, DescribeSubnets |
| **EC2 — Security Group** | CreateSecurityGroup, DeleteSecurityGroup, DescribeSecurityGroups, AuthorizeSecurityGroupIngress/Egress, RevokeSecurityGroupIngress/Egress |
| **EC2 — Internet Gateway** | CreateInternetGateway, AttachInternetGateway, DetachInternetGateway, DescribeInternetGateways |
| **EC2 — Route Table** | CreateRouteTable, DescribeRouteTables, CreateRoute (gateway/nat-gateway/peering targets) |
| **EC2 — EBS Volumes** | CreateVolume, DeleteVolume, DescribeVolumes, AttachVolume, DetachVolume |
| **EC2 — Key Pairs** | CreateKeyPair, DeleteKeyPair, DescribeKeyPairs |
| **Auto Scaling** | CreateAutoScalingGroup, UpdateAutoScalingGroup, DeleteAutoScalingGroup, DescribeAutoScalingGroups, SetDesiredCapacity, PutScalingPolicy, DeletePolicy, ExecutePolicy |
| **EC2 — Snapshots** | CreateSnapshot, DeleteSnapshot, DescribeSnapshots |
| **EC2 — AMIs** | CreateImage, DeregisterImage, DescribeImages |
| **EC2 — Spot Instances** | RequestSpotInstances, CancelSpotInstanceRequests, DescribeSpotInstanceRequests |
| **EC2 — Launch Templates** | CreateLaunchTemplate, DeleteLaunchTemplate, DescribeLaunchTemplates |
| **EC2 — NAT Gateways** | CreateNatGateway, DeleteNatGateway, DescribeNatGateways |
| **EC2 — VPC Peering** | CreateVpcPeeringConnection, AcceptVpcPeeringConnection, DeleteVpcPeeringConnection, DescribeVpcPeeringConnections |
| **EC2 — Flow Logs** | CreateFlowLogs, DeleteFlowLogs, DescribeFlowLogs |
| **EC2 — Network ACLs** | CreateNetworkAcl, DeleteNetworkAcl, DescribeNetworkAcls, CreateNetworkAclEntry, DeleteNetworkAclEntry |
| **CloudWatch** *(rpc-v2-cbor)* | PutMetricData, GetMetricStatistics, ListMetrics, PutMetricAlarm, DescribeAlarms, DeleteAlarms |

Any operation not in this list returns `501 Not Implemented` or the AWS-style `UnknownOperation` / `InvalidAction` error. The list grows each phase — see the bottom of this page.

## How it's wired internally

The server is a tiny core plus a plugin-per-service model. Each service is a self-contained package under `server/`.

```
server/
├── server.go                    # core: Handler interface + Server (≈80 LOC)
├── wire/
│   ├── wire.go                  # S3/DynamoDB shared helpers (XML, JSON, date)
│   └── awsquery/                # AWS query-protocol: form decoder + XML envelope
└── aws/
    ├── aws.go                   # convenience: New(Drivers{...}) *server.Server
    ├── s3/                      # S3 REST+XML handler
    ├── dynamodb/                # DynamoDB JSON-RPC handler
    └── ec2/                     # EC2 query-protocol handler
```

Each handler implements a two-method interface:

```go
type Handler interface {
    Matches(r *http.Request) bool                    // detect by header/path/form
    ServeHTTP(w http.ResponseWriter, r *http.Request)
}
```

`server.Server` iterates registered handlers and dispatches to the first that claims the request. Adding a new service (Lambda, SQS, Azure Blob, GCS) = a new package + one `Register` call. The core never changes.

### Protocol detection

Each handler uses a different signal so dispatch is unambiguous:

| Service | How it's detected |
|---------|-------------------|
| DynamoDB | `X-Amz-Target: DynamoDB_20120810.*` header |
| EC2 | `Action=…` in URL query or `Content-Type: application/x-www-form-urlencoded` POST |
| S3 | Fallback (everything else) |

## Roadmap

The EC2 SDK-compat work is Phase 1 of a larger initiative (tracked in [#121](https://github.com/stackshy/cloudemu/issues/121)):

| Phase | Scope |
|-------|-------|
| 1 (done) | Query-protocol foundation + EC2 core instance ops |
| 2 (done) | VPC, Subnets, Security Groups, Internet Gateways, Route Tables |
| 3 (done) | EBS Volumes, Key Pairs |
| 4 (done) | Auto Scaling Groups + scaling policies |
| 5 (done) | Snapshots + AMIs |
| 6 (done) | Spot Instances + Launch Templates |
| 7 (done) | NAT Gateways + VPC Peering + Flow Logs |
| 8 (done) | Network ACLs |
| 3 | EBS Volumes, Key Pairs |
| 4 | Auto-Scaling Groups + Scaling Policies |
| 5 | Snapshots, AMIs |
| 6 | Spot Instances, Launch Templates |
| 7 | NAT Gateways, VPC Peering, Flow Logs |
| 8 | Network ACLs |

After AWS is complete, the sibling `server/azure/` and `server/gcp/` packages will bring the same experience to Azure Blob / Cosmos and GCP GCS / Firestore.

## Writing your own handler

If you need a service we don't cover yet, implement the `server.Handler` interface in your own package and register it:

```go
type MyHandler struct{ /* driver */ }

func (*MyHandler) Matches(r *http.Request) bool {
    // your detection logic
}

func (h *MyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // your logic
}

srv := server.New()
srv.Register(&MyHandler{...})
```

The `Handler` interface is the only contract — no registration is needed in core CloudEmu. If the handler is generally useful, a PR to add it under `server/aws/<service>` is welcome.

## Limitations

- **No signature validation.** CloudEmu is a local development tool, not a security boundary. Requests are accepted regardless of AWS SigV4 signatures.
- **No pagination continuation** (S3 listing aside). Adding it as providers grow.
- **No presigned URLs, no multipart uploads** (yet) for S3.
- **No `UpdateItem`, `Scan`, `BatchWriteItem`, transactions** (yet) for DynamoDB.
- **No Auto-Scaling, Spot, Volumes, AMIs** (yet) for EC2 — see roadmap above.

When a client hits an unsupported operation, the server responds with a recognizable AWS error code so failures are easy to diagnose.
