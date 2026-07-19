# E2E Test-and-Fix Campaign

Goal: exercise every corner of cloudemu the way real users do — every service
domain × provider × surface (portable Go API + SDK-compat HTTP with the real
SDKs), plus every cross-cutting feature and feature×service composition.
Per cell: **capture operations → write user-journey E2E tests → run → triage
→ fix → re-test → review.**

- Branch: `campaign/e2e-full-matrix` (PR to `development` only with consent).
- Baseline at start: build green, 190 packages passing, 268 test files.
- One loop iteration ≈ one domain (all providers, both surfaces) or one
  feature. Each iteration runs as a multi-agent workflow: survey → test →
  triage/fix → review, and updates this file.

Test shape per cell (real-user journeys, not unit pokes):
1. Happy path lifecycle: create → use → list/get → update → delete.
2. Edge cases: not-found, duplicate create, delete-then-use, empty lists,
   pagination/continuation, validation errors, zero/large payloads.
3. Cross-op invariants: state machine transitions, TTL/dedup windows (with
   fake clock), metrics side-effects where the domain emits them.
4. SDK-compat cells drive the REAL SDK client end-to-end (httptest server),
   asserting decoded responses and SDK-visible error types.

Issue log convention: `[domain/provider/surface] symptom → root cause → fix commit`.

## Matrix status

Legend: [ ] pending · [~] in progress · [x] done (tested, issues fixed, reviewed)

### Domains
- [x] storage (S3 / BlobStorage / GCS) — iter 1: 49 e2e tests, 3 product bugs fixed, full suite green
- [x] database (DynamoDB / CosmosDB / Firestore) — iter 2: ~60 e2e tests, 1 systemic driver bug + 5 wire bugs fixed, full suite green
- [ ] messagequeue (SQS / ServiceBus / PubSub)
- [ ] compute (EC2 / VirtualMachines / GCE)
- [ ] serverless (Lambda / Functions / CloudFunctions)
- [ ] monitoring (CloudWatch / Monitor / CloudMonitoring)
- [ ] secrets (SecretsManager / KeyVault / SecretManager)
- [ ] cache (ElastiCache / Cache / Memorystore)
- [ ] iam (IAM / RBAC / IAM)
- [ ] networking (VPC / VNet / VPC)
- [ ] dns (Route53 / DNS / CloudDNS)
- [ ] loadbalancer (ELB / LB / LB)
- [ ] notification (SNS / NotificationHubs / FCM)
- [ ] eventbus (EventBridge / EventGrid / Eventarc)
- [ ] containerregistry (ECR / ACR / ArtifactRegistry)
- [ ] logging (CloudWatchLogs / LogAnalytics / CloudLogging)
- [ ] relationaldb (RDS·Redshift / SQL·Flex / CloudSQL)
- [ ] kubernetes (EKS / AKS / GKE + client-go data plane)
- [ ] resource-discovery (ResourceExplorer / ResourceGraph / AssetInventory)
- [ ] ai (Bedrock·SageMaker / — / VertexAI)
- [ ] databricks (— / Databricks / —)

### Features
- [ ] chaos (outages, latency windows, throttling — against live SDK traffic)
- [ ] recorder (capture + matcher assertions)
- [ ] metrics (collector + query)
- [ ] ratelimit (limiter under burst, 429/Throttled on both surfaces)
- [ ] inject (always / nth / probabilistic / countdown policies)
- [ ] fake clock (TTL expiry, dedup windows, alarm evaluation)
- [ ] latency (config.WithLatency global + per-service)
- [ ] topology (CanConnect / TraceRoute / Resolve across fixtures)

### Compositions
- [ ] chaos × sdk-compat (real SDK sees injected failures with correct wire errors)
- [ ] features stacked (recorder+metrics+ratelimit on one driver)
- [ ] fake clock × TTL-bearing domains (cache, database TTL, messagequeue dedup)

## Issue log

Iteration 1 — storage (49 new e2e tests across 6 cells; all fixed and reviewed):
- [storage/aws/sdk-compat] PutObject HTTP response omitted the ETag header (real S3 always returns it; SDK users read resp.ETag) → server/aws/s3/handler.go now returns the quoted sha256 ETag.
- [storage/azure/sdk-compat] Put Blob ignored `x-ms-blob-content-type`, storing application/octet-stream for SDK uploads → handler honors the header (request Content-Type kept as fallback for direct-HTTP callers).
- [storage/gcp/sdk-compat] Multipart upload parser used strings.TrimRight which ate legitimate trailing `-`/`\r\n` bytes from payloads → replaced with byte-exact mime/multipart parsing (RFC 2046).

Iteration 2 — database (portable + SDK cells across all 3 providers):
- [database/all/portable] SYSTEMIC: Scan/Query iterated a freshly-copied map (randomized order per call) then applied offset page tokens — multi-page reads duplicated and dropped items on ALL THREE drivers → stable sort by item key before pagination (dynamodb, cosmosdb, firestore drivers).
- [database/aws/sdk-compat] DynamoDB wire handler had no ExclusiveStartKey/LastEvaluatedKey support — SDK users could never page past page 1 → implemented key-based wire pagination over the driver's stable ordering (Query + Scan).
- [database/aws/sdk-compat] PutItem ignored ConditionExpression — attribute_not_exists create-if-absent silently overwrote → attribute_exists/attribute_not_exists enforced with ConditionalCheckFailedException; unsupported expressions return ValidationException.
- [database/azure/sdk-compat] Continuation-page query requests (marked only by Content-Type application/query+json) were misrouted as document creates → 400 "item must contain an id field"; and the query path had no x-ms-max-item-count / x-ms-continuation support → both fixed.
- [database/gcp/sdk-compat] batchGet emitted one JSON array PER entry — clients decoded only the first document → single-array response.
- [database/gcp/sdk-compat] :commit dropped currentDocument preconditions — Create on existing doc succeeded, Update on missing doc upserted → exists preconditions enforced (409 ALREADY_EXISTS / 404 NOT_FOUND).
- [test-helper] REST transport folds HTTP 409 into gRPC Aborted; dbSDKCode now prefers the concrete googleapi HTTP status.

## Human decisions needed (deviations locked in as documented behavior, flagged by review)
- S3 DeleteObject on a missing key returns 404 NoSuchKey; real S3 is idempotent 204 — breaks defensive-delete code.
- Missing bucket maps to NoSuchKey; real S3 returns NoSuchBucket.
- Deleting a non-empty bucket/container returns 500 InternalError (AWS + Azure); real clouds return 409 — and 5xx triggers SDK retry backoff.
- Azure putBlob returns a hard-coded ETag ("0x8DAB0") and wall-clock LastModified.
- server/aws/s3 putObject recomputes sha256 inline, duplicating the driver's ETag algorithm (drift risk).
- Database driver QueryInput.ScanForward is accepted but never applied (no descending order support in any provider driver).
- Azure Cosmos multipart-order asymmetry: Azure/GCP mocks assemble multipart parts in caller order; S3 sorts by part number.
