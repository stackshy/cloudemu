# OCI Service Conventions

The contract every OCI service implementation follows. One service per branch,
one PR to `development`. Read this before writing a service; it exists so
seventeen independently-built services come out looking like one codebase.

The foundation this builds on (`config` identity, `internal/idgen` OCIDs,
`scope.Compartment`, `server/wire/ocirest`, `server/oci/workrequest`, the
`Provider` and `Drivers` bundles) is already on `development`. Do not modify it
without saying so in your PR — every other service depends on it.

## Scope of one branch

| Branch | Adds |
|---|---|
| `feat/oci-<service>` | `providers/oci/<service>/` and `server/oci/<service>/` |

Assign your slot in `providers/oci/oci.go` `New()` and `server/oci/oci.go`
`New()`, and map it in `server/oci/from_provider.go`. The struct fields already
exist — fill them in, do not restructure them. Those three one-line edits are
the only shared files a service branch touches.

## The three layers

A service implements the portable driver interface that already exists in
`services/<name>/driver`. Do not define a new driver interface; if OCI needs an
operation the interface lacks, add it as an optional capability discovered by
type assertion, the way `storage/driver.BucketAttributes` and
`cache/driver.SubnetGroups` do.

| Layer | Path | Contains |
|---|---|---|
| Provider mock | `providers/oci/<service>/` | `Mock` over `memstore.Store[V]`, implements the driver |
| Wire handler | `server/oci/<service>/` | `Matches`/`ServeHTTP`, speaks OCI REST |

Start the mock with a compile-time interface check, as every other provider does:

```go
var _ driver.Bucket = (*Mock)(nil)
```

## Identity

Read identity off `*config.Options`. Never hard-code a tenancy, compartment or
realm.

| Field | Meaning |
|---|---|
| `o.TenancyOCID` | Root compartment |
| `o.CompartmentID` | Where a resource lands when the caller names none |
| `o.Realm` | `oc1` commercial, `oc2`/`oc3` government |
| `o.OCIRegion()` | Region, substituting an OCI region for the AWS default |

Use `o.OCIRegion()`, not `o.Region`. The latter defaults to `us-east-1`, which
is not an OCI region and mints OCIDs with a nonsense region code.

## OCIDs

Generate every identifier with `idgen`, never by hand.

```go
idgen.OCID("instance", o.Realm, o.OCIRegion())  // ocid1.instance.oc1.iad.aaaaaaaa…
idgen.GlobalOCID("compartment", o.Realm)        // ocid1.compartment.oc1..aaaaaaaa…
```

Identity resources — compartments, users, groups, policies, dynamic groups —
are region-agnostic and use `GlobalOCID`. Everything else is region-scoped.
The resource type segment is the lowercase OCI resource name (`instance`,
`vcn`, `subnet`, `bucket`, `vault`, `cluster`).

## Compartments

`compartmentId` is required on nearly every OCI list call. Record it at create
time and filter lists by it:

```go
scope.Scope{Compartment: compartmentID}
```

Matching is exact — real OCI only descends the compartment tree when the caller
passes `compartmentIdInSubtree=true`. Handlers get the parameter with
`ocirest.RequireCompartmentID(w, r)`, which writes the 400 for you when it is
missing. Use it on every list endpoint; do not fall back to listing across all
compartments, which real OCI never does.

## Wire handlers

Use `server/wire/ocirest` for everything that touches the response:

| Helper | Use |
|---|---|
| `WriteJSON(w, r, status, v)` | Success. Stamps `opc-request-id` |
| `WriteDriverError(w, r, err)` | Driver errors. Maps the canonical code to OCI's status and code |
| `WriteError(w, r, status, code, msg)` | Errors raised in the handler itself |
| `DecodeJSON(w, r, &v)` | Request bodies. Returns false once it has written the 400 |
| `RequireCompartmentID(w, r)` | The required list parameter |
| `Limit(r)` / `Page(r)` / `SetNextPage(w, tok)` | Pagination. `Limit` caps at `MaxLimit` |
| `SetWorkRequestID(w, id)` | Async mutations |

The write helpers take the `*http.Request` so they can echo the caller's
`opc-request-id`, which SDKs and logs correlate on, and mint one only when the
caller sent none. Pass the request through; do not pass nil.

Never write an error body by hand. `WriteDriverError` is what keeps
`NotFound` and `PermissionDenied` collapsing into the single
`404 NotAuthorizedOrNotFound` that real OCI returns, so callers cannot probe
across a compartment boundary.

Do not verify request signatures. No provider in this repo authenticates, and
the OCI SDK's signing is ignored the same way.

### Matches

Register the narrowest predicate that identifies your service. Handlers are
evaluated in registration order and first match wins, so a broad `Matches` will
silently swallow another service's traffic. If your paths overlap another
service's, say so in the `Drivers` field comment — that is what the GCP bundle
does for AlloyDB and GKE.

## Work requests

Mutations that are asynchronous in real OCI return `202` with an
`opc-work-request-id` header. Record one and stamp it:

```go
id := d.WorkRequests.Accept("CREATE_INSTANCE", compartmentID, workrequest.Resource{
    EntityType: "instance",
    ActionType: workrequest.ActionCreated,
    Identifier: inst.ID,
})
ocirest.SetWorkRequestID(w, id)
ocirest.WriteJSON(w, r, http.StatusAccepted, nil)
```

Every CloudEmu mutation completes synchronously, so the request is already
`SUCCEEDED` when the waiter first polls. The envelope exists to satisfy SDK
waiters and to carry the created resource's OCID back to them. The shared
poller is registered ahead of every service handler; do not serve
`/workRequests` yourself.

## Metrics

If the service produces metrics, give the mock a `SetMonitoring` method:

```go
func (m *Mock) SetMonitoring(mon mondriver.Monitoring) { m.mon = mon }
```

`providers/oci.New` finds it by type assertion and wires it. Add the service to
the slice in `wireMonitoring` if it belongs there.

## Errors

Return `errors.New(errors.<Code>, msg)` / `errors.Newf` from drivers. Use the
canonical codes — the wire layer maps them. Do not return OCI error strings
from a driver; the driver is provider-agnostic and the same code path serves
the portable API.

## Comments

Match the density of the file next to yours. Struct fields are usually bare;
where a comment earns its place it is one or two lines stating a fact. Type and
function doc comments are typically a single sentence. No multi-paragraph
rationale blocks, no embedded code samples in doc comments.

## Tests

`go build ./... && go test ./...` and `golangci-lint run` must be clean before
the PR. Table-driven tests with `testify`, matching the existing files.

Cover, at minimum:

- Driver CRUD, including the not-found and already-exists paths
- Compartment filtering — a resource in another compartment must not list
- OCID shape for each resource type the service mints
- Handler routing: what `Matches` claims and, importantly, what it does not
- The wire response for one success and one error per operation family

An SDK-compat test using `github.com/oracle/oci-go-sdk` against
`httptest.NewServer` is the strongest evidence the handler is right. Add one
where the SDK makes it practical.

## Definition of done

- [ ] Driver interface fully implemented, compile-time check present
- [ ] Wire handler registered in `server/oci/oci.go`
- [ ] Slot assigned in `providers/oci/oci.go` and `server/oci/from_provider.go`
- [ ] `go build ./...` clean
- [ ] `go test ./...` clean — the whole suite, not just your package
- [ ] `golangci-lint run` clean for the packages you touched
- [ ] Operations added to `docs/services.md`
