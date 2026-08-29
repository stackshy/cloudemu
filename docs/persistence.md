# Persistence (snapshot & restore)

CloudEmu keeps all state in memory, so by default it is **ephemeral**: everything
you create is lost when the process exits, and `/_cloudemu/reset` wipes it back to
empty. Persistence is **opt-in** — CloudEmu never writes to disk unless you ask it
to. When you do, it captures the **whole emulator** as a single JSON document and
restores it into a fresh instance.

Two properties make this more than a naive dump:

- **Full-surface.** Every stateful service — one that holds in-memory state —
  across **AWS, Azure, GCP, and OCI** is captured, not a hand-picked subset. A
  completeness guard (`persist/completeness_test.go`) fails the build if a new
  stateful service is added without persistence, so coverage can't silently drift.
- **Identity-preserving.** Resource IDs and the ID-string cross-references between
  resources (e.g. an instance's subnet/VPC, a secret's KMS key) are serialized
  as-is, so a snapshot → restore round-trip is transparent to clients — a restored
  EC2 instance keeps its `i-…` ID, not a freshly minted one.

The snapshot is one human-readable, `git`-diffable JSON file that spans every
provider (schema version 3).

## Which surface should I use?

| You want to… | Use |
|--------------|-----|
| Keep resources across a background-server `stop`→`start` | `cloudemu start --persist` |
| Capture/name/switch between multiple states on a running server | `cloudemu snapshot save`/`load`/`list`/`delete` |
| Export or replace the whole state over HTTP | `GET`/`POST /_cloudemu/snapshot` |
| Save/restore state from Go code | the [`persist`](https://pkg.go.dev/github.com/stackshy/cloudemu/v2/persist) package |

## `--persist` (auto save on stop, restore on start)

Pass `--persist` to the background server and your resources survive a
`stop`→`start` cycle:

```sh
cloudemu start --persist          # save on stop, restore on start
# create buckets / tables / instances / secrets …
cloudemu stop                     # writes <run-dir>/snapshot.json
cloudemu start --persist          # your resources are back, same IDs
cloudemu delete                   # also removes the snapshot
```

`start` manages the snapshot path for you. For the foreground server the path is
explicit and required:

```sh
cloudemu serve --persist --state-file ./state.json
cloudemu serve --persist --state-file ./state.json --persist-metadata-only  # skip object bodies
```

By default the snapshot **includes object bodies** (an S3 object comes back with
its contents). `--persist-metadata-only` keeps only the resource structure for a
smaller file; restored objects then come back as zero-byte keys until re-uploaded.

Snapshot writes are **crash-safe**: the file is written to a temp file, `fsync`ed,
`rename`d atomically onto the target, and the parent directory is then `fsync`ed —
so an interrupted or power-lost write leaves the previous snapshot (or none) but
never a truncated/empty state file. On **macOS** this is best-effort: Go's
`File.Sync` issues `fsync(2)`, which does not flush the drive's own write cache
(a true device flush needs `fcntl(F_FULLFSYNC)`, not issued here), so darwin gives
no hard power-loss guarantee.

## Named snapshots (`snapshot save` / `load` / `list` / `delete`)

Where `--persist` auto-saves one state on stop, **named snapshots** capture, name,
and switch between *multiple* states on a running server — a local, free
equivalent of LocalStack's Cloud Pods:

```sh
cloudemu snapshot save baseline     # capture current state as "baseline"
# … run a destructive test …
cloudemu snapshot load baseline     # restore it instantly — no restart
cloudemu snapshot list              # NAME  CREATED  PROVIDERS  SIZE
cloudemu snapshot delete baseline
```

Each is a JSON file under `~/.cloudemu/snapshots/<name>.json` (override the dir
with `--home`) — inspectable, `git`-diffable, and shareable: copy the file to a
teammate and they `snapshot load` the identical state. Names match
`[A-Za-z0-9._-]` (1–64 chars).

`save`/`load` talk to the running server's control plane, so they need the
`--admin` plane (on by default) and the `aws` or `gcp` provider running; `list`
and `delete` are file operations that work without a running server. `load` is
destructive — it wipes the running state (reset semantics) and repopulates from
the snapshot, so anything created since the snapshot is discarded.

## Admin endpoint (`/_cloudemu/snapshot`)

The control plane exposes the same capability over HTTP, acting on every provider
at once (like `reset`), so a call to any provider port covers the whole emulator:

```sh
# export the whole-emulator state as JSON
curl http://127.0.0.1:4566/_cloudemu/snapshot > state.json

# replace the whole-emulator state from a JSON document
curl -X POST http://127.0.0.1:4566/_cloudemu/snapshot --data @state.json
```

Both are disabled (`501`) when the server is started with `--admin=false`. A
POST larger than 512 MiB is rejected. See the control-plane section of
[standalone-server.md](standalone-server.md#resetting-state-between-tests-_cloudemu)
for the neighbouring `reset`/`seed` endpoints.

## Go API (`persist` package)

In-process code drives the same machinery directly. Each provider factory exposes
`SnapshotServices()`, which auto-discovers the services that can snapshot
themselves; `ExportAll`/`RestoreAll` operate over a `provider → services` map:

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

`persist.Options{IncludeAssets: false}` (the default) yields a metadata-only
snapshot. `ReadFile` rejects an incompatible schema version with a clear error
rather than mis-restoring a stale layout. Restore targets should be freshly built
(empty) before you restore into them.

## Notes

- Persistence is a **dev-only convenience**. The on-disk schema can change between
  CloudEmu versions; an incompatible snapshot is rejected with a clear error, so
  re-create snapshots after upgrading rather than porting old ones.
- Seed fixtures ([`/_cloudemu/seed`](standalone-server.md#resetting-state-between-tests-_cloudemu)
  and the [`seed`](https://pkg.go.dev/github.com/stackshy/cloudemu/v2/seed)
  package) are the complementary tool for standing up a **known baseline** from a
  declarative file, rather than replaying a captured live state.
