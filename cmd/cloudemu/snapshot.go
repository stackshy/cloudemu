//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/stackshy/cloudemu/v2/persist"
)

const (
	snapshotsDirName = "snapshots"
	snapHTTPTimeout  = 30 * time.Second
)

var snapNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// Sentinel errors so callers (and err113) get static, wrappable failures.
var (
	errSnapUsage      = errors.New("usage: cloudemu snapshot <save|load|list|delete> [name] [--home dir] [--force]")
	errSnapName       = errors.New("snapshot name must be 1-64 chars of [A-Za-z0-9._-], excluding the reserved dot and double-dot")
	errSnapExists     = errors.New("snapshot already exists (pass --force to overwrite)")
	errSnapNotFound   = errors.New("snapshot not found")
	errSnapNoEndpoint = errors.New("no plain-HTTP endpoint available (start the aws or gcp provider)")
	errSnapAdminOff   = errors.New("the server's control plane is disabled (started with --admin=false)")
	errSnapDaemonDown = errors.New("cloudemu is not running (snapshot save/load need a running server)")
	errSnapServer     = errors.New("snapshot request failed")
)

func snapshotsDir(dir string) string        { return filepath.Join(dir, snapshotsDirName) }
func snapshotFilePath(dir, n string) string { return filepath.Join(snapshotsDir(dir), n+".json") }

func validSnapshotName(name string) bool {
	return name != "." && name != ".." && snapNameRE.MatchString(name)
}

// parseSnapshotFlags splits --home / --force out of args, returning the
// remaining positional args.
func parseSnapshotFlags(args []string) (home string, force bool, pos []string) {
	home, rest := splitHomeFlag(args)
	pos = make([]string, 0, len(rest))

	for _, a := range rest {
		if a == "--force" || a == "-force" {
			force = true

			continue
		}

		pos = append(pos, a)
	}

	return home, force, pos
}

// runSnapshot dispatches the snapshot save/load/list/delete subcommands.
func runSnapshot(args []string) error {
	if len(args) == 0 {
		return errSnapUsage
	}

	home, force, pos := parseSnapshotFlags(args[1:])

	dir, err := runDir(home)
	if err != nil {
		return err
	}

	switch args[0] {
	case "save":
		return withName(pos, func(n string) error { return snapshotSave(dir, n, force) })
	case "load":
		return withName(pos, func(n string) error { return snapshotLoad(dir, n) })
	case "delete":
		return withName(pos, func(n string) error { return snapshotDelete(dir, n) })
	case "list":
		return snapshotList(dir)
	default:
		return errSnapUsage
	}
}

// withName runs fn with the single positional name, or returns a usage error.
func withName(pos []string, fn func(string) error) error {
	if len(pos) != 1 {
		return errSnapUsage
	}

	return fn(pos[0])
}

func snapshotSave(dir, name string, force bool) error {
	if !validSnapshotName(name) {
		return errSnapName
	}

	path := snapshotFilePath(dir, name)
	if !force {
		if _, err := os.Stat(path); err == nil {
			return errSnapExists
		}
	}

	base, err := adminBaseURL(dir)
	if err != nil {
		return err
	}

	body, err := snapshotRequest(http.MethodGet, base, nil)
	if err != nil {
		return err
	}

	var snap persist.Snapshot
	if err := json.Unmarshal(body, &snap); err != nil {
		return fmt.Errorf("parse snapshot from server: %w", err)
	}

	providers := make([]string, 0, len(snap.Providers))
	for p := range snap.Providers {
		providers = append(providers, p)
	}

	sort.Strings(providers)

	snap.Meta = &persist.Meta{
		Name:            name,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		CloudemuVersion: version,
		Providers:       providers,
	}

	if err := snap.WriteFile(path); err != nil {
		return err
	}

	fmt.Printf("saved snapshot %q (providers: %s)\n", name, strings.Join(providers, ", "))

	return nil
}

func snapshotLoad(dir, name string) error {
	if !validSnapshotName(name) {
		return errSnapName
	}

	body, err := os.ReadFile(snapshotFilePath(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return errSnapNotFound
	}

	if err != nil {
		return err
	}

	base, err := adminBaseURL(dir)
	if err != nil {
		return err
	}

	if _, err := snapshotRequest(http.MethodPost, base, body); err != nil {
		return err
	}

	fmt.Printf("loaded snapshot %q\n", name)

	return nil
}

func snapshotDelete(dir, name string) error {
	if !validSnapshotName(name) {
		return errSnapName
	}

	err := os.Remove(snapshotFilePath(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return errSnapNotFound
	}

	if err != nil {
		return err
	}

	fmt.Printf("deleted snapshot %q\n", name)

	return nil
}

func snapshotList(dir string) error {
	entries, err := os.ReadDir(snapshotsDir(dir))
	if errors.Is(err, os.ErrNotExist) {
		fmt.Println("no snapshots")

		return nil
	}

	if err != nil {
		return err
	}

	printed := 0

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		info, iErr := e.Info()
		if iErr != nil {
			return iErr
		}

		if printed == 0 {
			fmt.Printf("%-24s %-20s %-18s %10s\n", "NAME", "CREATED", "PROVIDERS", "SIZE")
		}

		name, created, providers := snapshotRow(snapshotsDir(dir), e, info)
		fmt.Printf("%-24s %-20s %-18s %10d\n", name, created, providers, info.Size())

		printed++
	}

	if printed == 0 {
		fmt.Println("no snapshots")
	}

	return nil
}

// snapshotRow derives the display columns for one snapshot file, preferring the
// stored Meta header (accurate created-at + captured providers) and falling
// back to the filename / file mtime when Meta is absent or unreadable.
func snapshotRow(dir string, e os.DirEntry, info os.FileInfo) (name, created, providers string) {
	name = strings.TrimSuffix(e.Name(), ".json")
	created = info.ModTime().UTC().Format(time.DateTime)
	providers = "-"

	meta := readSnapshotMeta(filepath.Join(dir, e.Name()))
	if meta == nil {
		return name, created, providers
	}

	if meta.Name != "" {
		name = meta.Name
	}

	if meta.CreatedAt != "" {
		created = meta.CreatedAt
	}

	if len(meta.Providers) > 0 {
		providers = strings.Join(meta.Providers, ",")
	}

	return name, created, providers
}

// readSnapshotMeta returns the Meta header of a snapshot file, or nil if the
// file can't be read or parsed.
func readSnapshotMeta(path string) *persist.Meta {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var s persist.Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil
	}

	return s.Meta
}

// adminBaseURL reads the daemon's endpoints file and returns a plain-HTTP base
// URL for the control plane (avoids the self-signed HTTPS endpoints).
func adminBaseURL(dir string) (string, error) {
	eps, err := readEndpoints(endpointsPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return "", errSnapDaemonDown
	}

	if err != nil {
		return "", err
	}

	for _, k := range []string{"aws", "gcp"} {
		if ep := eps[k]; strings.HasPrefix(ep, "http://") {
			return strings.TrimRight(ep, "/"), nil
		}
	}

	return "", errSnapNoEndpoint
}

// snapshotRequest calls the daemon's /_cloudemu/snapshot endpoint. For GET body
// is nil and the response bytes are returned; for POST body is the snapshot.
func snapshotRequest(method, base string, body []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), snapHTTPTimeout)
	defer cancel()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, base+"/_cloudemu/snapshot", reader)
	if err != nil {
		return nil, err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errSnapDaemonDown, err)
	}
	defer resp.Body.Close()

	rb, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusNotImplemented {
		return nil, errSnapAdminOff
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s: %s", errSnapServer, resp.Status, strings.TrimSpace(string(rb)))
	}

	return rb, nil
}
