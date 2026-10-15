// Package projectctx carries the GCP project a caller addressed on a request
// context, so in-memory backends keep each project's resources apart the way
// real GCP does.
//
// It is the GCP twin of internal/regionctx. Each GCP handler stamps the
// project it parsed from the request path; providers read it back with
// ProjectOr, falling back to their configured default project (opts.ProjectID)
// when nothing was stamped. That fallback keeps the typed Go API acting on the
// default project. Every operation, lists included, acts on one project. Only
// a context marked with AllProjects sees every project: resource discovery,
// Cloud Asset, cost and in-use guards use it explicitly.
package projectctx

import (
	"context"
	"log"
	"strings"
)

type projectKey struct{}

type allKey struct{}

// WithProject returns a copy of ctx carrying project. The empty string and the
// GCP wildcards "_" and "-" are not stored, so ProjectOr falls back to the
// caller's default.
func WithProject(ctx context.Context, project string) context.Context {
	if project == "" || project == "_" || project == "-" {
		return ctx
	}

	return context.WithValue(ctx, projectKey{}, project)
}

// Lookup returns the project stamped on ctx, if any.
func Lookup(ctx context.Context) (string, bool) {
	p, ok := ctx.Value(projectKey{}).(string)

	return p, ok && p != ""
}

// ProjectOr returns the project stamped on ctx, or fallback when none is
// present.
func ProjectOr(ctx context.Context, fallback string) string {
	if p, ok := Lookup(ctx); ok {
		return p
	}

	return fallback
}

// AllProjects returns a copy of ctx that asks list and scan operations to
// cover every project instead of one.
func AllProjects(ctx context.Context) context.Context {
	return context.WithValue(ctx, allKey{}, true)
}

// IsAllProjects reports whether ctx was marked with AllProjects.
func IsAllProjects(ctx context.Context) bool {
	all, _ := ctx.Value(allKey{}).(bool)

	return all
}

// Key joins a project and a project-local name into the one key shape used by
// project-scoped stores.
func Key(project, local string) string {
	return project + "/" + local
}

// Split is the inverse of Key. ok is false for a key with no project part,
// which is the shape of records stored before project scoping.
func Split(key string) (project, local string, ok bool) {
	i := strings.IndexByte(key, '/')
	if i < 0 {
		return "", key, false
	}

	return key[:i], key[i+1:], true
}

// FromPath returns the project of the first "projects/{p}" pair in a URL, a
// URL path or a resource name, or "" when there is none.
func FromPath(p string) string {
	parts := strings.Split(p, "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "projects" && parts[i+1] != "" {
			return parts[i+1]
		}
	}

	return ""
}

// WarnAdopted logs, once per service restore, how many records from a snapshot
// taken before project scoping were adopted into the default project. Nothing
// is logged when n is zero.
func WarnAdopted(service string, n int, project string) {
	if n == 0 {
		return
	}

	log.Printf("gcp/%s: adopted %d legacy project-less records into project %s "+
		"(set --project-id to the project your clients use)", service, n, project)
}
