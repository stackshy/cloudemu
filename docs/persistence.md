# Persistence (snapshot & restore)

CloudEmu keeps all state in memory. By default, everything you create is lost
when the process exits, and `/_cloudemu/reset` empties it. Persistence is opt-in:
CloudEmu doesn't write to disk unless you ask it to. When you do, it saves the
whole emulator as one JSON document and can restore it into a fresh instance.

Two things to know about it:

- It covers every stateful service (any service that holds in-memory state) in
  AWS, Azure, GCP and OCI. A completeness test (`persist/completeness_test.go`)
  fails the build if someone adds a stateful service without persistence support.
- It keeps IDs. Resource IDs and the ID references between resources (e.g. an
  instance's subnet/VPC, a secret's KMS key) are written as-is, so clients see the
  same resources after a restore. A restored EC2 instance keeps its original `i-…`
  ID.

The snapshot is one readable JSON file that covers every provider plus the shared
Kubernetes data plane, and it diffs cleanly in version control (schema version 5).

## Which surface should I use?

| You want to… | Use |
|--------------|-----|
| Keep resources across a background-server `stop`→`start` | `cloudemu start --persist` |
| Capture/name/switch between multiple states on a running server | `cloudemu snapshot save`/`load`/`list`/`delete` |
| Export or replace the whole state over HTTP | `GET`/`POST /_cloudemu/snapshot` |
| Save / rewind / fork named states over HTTP (time travel) | `POST /_cloudemu/snapshot/{name}` · `…/{name}/rewind` · `…/{from}/fork/{to}` |
| Save/restore state from Go code | the [`persist`](https://pkg.go.dev/github.com/stackshy/cloudemu/v2/persist) package |

## `--persist` (always-on durability)

With `--persist`, the background server keeps your resources across a
`stop`→`start` cycle and across a crash:

```sh
cloudemu start --persist          # save periodically + on stop, restore on start
# create buckets / tables / instances / secrets …
cloudemu stop                     # writes <run-dir>/snapshot.json
cloudemu start --persist          # your resources are back, same IDs
cloudemu delete                   # also removes the snapshot
```

`start` manages the snapshot path for you. For the foreground server you must
give the path:

```sh
cloudemu serve --persist --state-file ./state.json
cloudemu serve --persist --state-file ./state.json --persist-metadata-only  # skip object bodies
```

By default the snapshot includes object bodies (an S3 object comes back with
its contents). `--persist-metadata-only` keeps only the resource structure for a
smaller file; restored objects then come back as zero-byte keys until re-uploaded.

### Save strategy (`--persist-strategy`, `--persist-interval`)

By default `--persist` saves in the background while the server runs. A `kill -9`,
panic, OOM or power loss then loses at most the changes since the last save, not
everything since boot. Choose when to save with `--persist-strategy` (env
`CLOUDEMU_PERSIST_STRATEGY`):

| Strategy | When it saves | Loss window on a hard crash |
|----------|---------------|-----------------------------|
| `scheduled` *(default)* | every `--persist-interval` (default `15s`) if state changed, plus on shutdown | ≤ interval |
| `on-request` | shortly after mutations settle, and at least once per second under a continuous write stream, plus on shutdown | ≤ ~1s |
| `on-shutdown` | only on graceful shutdown (the pre-1.x behavior) | everything since boot |
| `manual` | never automatically, only via `snapshot`/`POST /_cloudemu/snapshot` | everything not manually saved |

```sh
cloudemu serve --persist --state-file ./state.json                        # scheduled, 15s
cloudemu serve --persist --state-file ./state.json --persist-interval 5s  # scheduled, 5s
cloudemu serve --persist --state-file ./state.json --persist-strategy on-request
cloudemu serve --persist --state-file ./state.json --persist-strategy manual
```

The strategy flags only apply with `--persist` (a warning is printed if they are
set without it). `cloudemu serve` and the `cloudemu-server` binary (which bundles
the real engines) accept the same flags and env vars.

Details:

- Saves run on a background goroutine, never in the request path. So `on-request`
  (unlike LocalStack's `ON_REQUEST`) never blocks a client call.
- Only one save runs at a time. Triggers that arrive during a save are merged and
  checked again when it finishes, so saves slower than the interval don't pile up.
- Every save (background, on-request and shutdown) includes object bodies by
  default, so an S3 object survives a crash with its contents instead of coming
  back as an empty key. LocalStack behaves the same way. Pass
  `--persist-metadata-only` to leave bodies out of every save for a smaller, faster
  snapshot; restored objects then come back as zero-byte keys until re-uploaded.

### Crash-safe writes

Each save is atomic. The file is written to a temp file, `fsync`ed, `rename`d onto
the target, and then the parent directory is `fsync`ed. An interrupted write or
power loss leaves the previous snapshot (or none), never a truncated or empty state
file. On macOS this is best-effort: Go's `File.Sync` issues `fsync(2)`, which
doesn't flush the drive's own write cache. A full device flush needs
`fcntl(F_FULLFSYNC)`, which isn't issued here, so there is no hard power-loss
guarantee on darwin.

### Kubernetes data-plane

The shared Kubernetes data plane is persisted too. Everything a client created
through an EKS/AKS/GKE cluster's kubeconfig endpoint survives a stop/start and a
crash: Namespaces, Pods, Deployments, Services and their Endpoints,
ConfigMaps/Secrets, every registry-backed kind (ReplicaSets, Jobs, Ingresses, …),
and CustomResourceDefinitions with their custom resources. State is keyed by the
cluster UID that the kubeconfig embeds. After a restart, `DescribeCluster` returns
the same `…/k8s/<uid>` endpoint and it still serves the pods and deployments it had
before, so a kubeconfig saved before the restart keeps working unchanged. The
cluster-wide `resourceVersion` and the Service ClusterIP / Pod IP allocators are
restored as well, so a `kubectl` create after the restore continues from the
restored state instead of colliding with it.

Limitation: watches don't resume. A restart drops the TLS connection, so a
`kubectl` or informer watch that was open before the crash reconnects and relists
(client-go's normal behavior on a dropped watch) instead of resuming from its last
`resourceVersion`. The data plane keeps no watch-event history across restarts. The
relist returns the restored state correctly; only the in-flight event stream is
lost.

### vs LocalStack

LocalStack offers state persistence only in its paid tier. In CloudEmu it is
free. The strategies follow LocalStack's `SNAPSHOT_SAVE_STRATEGY`
(`SCHEDULED`/`ON_REQUEST`/`ON_SHUTDOWN`/`MANUAL`), so they should be familiar.

## Named snapshots (`snapshot save` / `load` / `list` / `delete`)

`--persist` keeps one state. Named snapshots let you save several states on a
running server and switch between them, similar to LocalStack's Cloud Pods:

```sh
cloudemu snapshot save baseline     # capture current state as "baseline"
# … run a destructive test …
cloudemu snapshot load baseline     # restore it, no restart needed
cloudemu snapshot list              # NAME  CREATED  PROVIDERS  SIZE
cloudemu snapshot delete baseline
```

Each snapshot is a JSON file under `~/.cloudemu/snapshots/<name>.json` (change
the directory with `--home`). You can read it, diff it, and share it: copy the file
to a teammate and `snapshot load` gives them the same state. Names match
`[A-Za-z0-9._-]` (1 to 64 chars).

`save` and `load` talk to the running server's control plane, so they need the
`--admin` plane (on by default) and the `aws` or `gcp` provider running. `list`
and `delete` are file operations and work without a server. `load` is
destructive: it wipes the running state (like reset) and repopulates it from the
snapshot, so anything created since the snapshot is lost.

## Admin endpoint (`/_cloudemu/snapshot`)

The same feature is available over HTTP. Like `reset`, it acts on every provider
at once, so a call to any provider port covers the whole emulator:

```sh
# export the whole-emulator state as JSON
curl http://127.0.0.1:4566/_cloudemu/snapshot > state.json

# replace the whole-emulator state from a JSON document
curl -X POST http://127.0.0.1:4566/_cloudemu/snapshot --data @state.json
```

Both return `501` when the server is started with `--admin=false`. A POST larger
than 512 MiB is rejected. See the control-plane section of
[standalone-server.md](standalone-server.md#resetting-state-between-tests-_cloudemu)
for the related `reset`/`seed` endpoints.

### Time travel (`/_cloudemu/snapshot/{name}`)

The named-snapshot registry (`server/serverkit/timetravel.go`) adds save, rewind
and fork on top of the same capture/restore. It is exposed over HTTP, so any
client can use it, not only the `cloudemu snapshot` CLI:

```sh
curl -X POST   http://127.0.0.1:4566/_cloudemu/snapshot/baseline           # save current state as "baseline"
curl -X POST   http://127.0.0.1:4566/_cloudemu/snapshot/baseline/rewind    # restore it (destructive)
curl -X POST   http://127.0.0.1:4566/_cloudemu/snapshot/baseline/fork/exp  # branch it to a new name
curl -X DELETE http://127.0.0.1:4566/_cloudemu/snapshot/exp                # drop a saved state
```

`rewind` wipes the running state and repopulates it from the named checkpoint
(the same as `snapshot load`). `fork` copies a checkpoint to a new name, so you can
start several experiments from one baseline. Like the endpoints above, these need
the `--admin` plane.

## Go API (`persist` package)

In-process Go code can call the same functions directly. Each provider exposes
`SnapshotServices()`, which finds the services that can snapshot themselves.
`ExportAll`/`RestoreAll` take a `provider → services` map:

```go
import (
    cloudemu "github.com/stackshy/cloudemu/v2"
    "github.com/stackshy/cloudemu/v2/persist"
)

aws := cloudemu.NewAWS()
targets := map[string]persist.Services{"aws": aws.SnapshotServices()}

// Capture and write to disk.
snap, _ := persist.ExportAll(ctx, targets, persist.Options{IncludeAssets: true})
_ = snap.WriteFile("state.json")

// Later, into a freshly built provider set:
fresh := cloudemu.NewAWS()
loaded, _ := persist.ReadFile("state.json")
_ = persist.RestoreAll(ctx, &loaded, map[string]persist.Services{"aws": fresh.SnapshotServices()})
```

`persist.Options{IncludeAssets: false}` (the default) gives a metadata-only
snapshot. `ReadFile` returns a clear error for an incompatible schema version
instead of restoring an old layout incorrectly. Restore into freshly built (empty)
providers.

## Notes

- Persistence is meant for development. The on-disk schema can change between
  CloudEmu versions, and an incompatible snapshot is rejected with an error, so
  re-create snapshots after upgrading instead of porting old ones.
- Seed fixtures ([`/_cloudemu/seed`](standalone-server.md#resetting-state-between-tests-_cloudemu)
  and the [`seed`](https://pkg.go.dev/github.com/stackshy/cloudemu/v2/seed)
  package) are the other option: they build a known baseline from a declarative
  file instead of restoring a captured state.
