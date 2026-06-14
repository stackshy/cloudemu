// Package resourcegraph serves Azure Resource Graph (armresourcegraph) REST
// requests against a *resourcediscovery.Engine.
//
// Resource Graph queries use Kusto Query Language (KQL), a rich
// data-analytics grammar. This handler implements a documented subset that
// covers the queries real callers make for inventory/discovery:
//
//	Resources
//	| where type == 'microsoft.compute/virtualmachines'
//	| where type =~ 'microsoft.network/virtualnetworks'
//	| where resourceGroup == 'rg-prod'
//	| where location == 'eastus'
//	| where tags['env'] == 'prod'
//	| where tags.env == 'prod'
//	| project id, type, name              (column selection: ignored, full
//	                                        records returned)
//	| limit 100                           (also: | take 100)
//
// Unknown tokens and unsupported clauses are tolerated: the parser logs
// them away and continues with the constraints it did understand. Real
// Resource Graph would reject malformed queries; the stub favors returning
// some result over a 400 so SDK callers that probe with unknown queries
// don't blow up.
package resourcegraph

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/stackshy/cloudemu/resourcediscovery"
)

// Canonical Azure resource type strings used by Resource Graph query syntax
// and emitted in response rows.
const (
	azureTypeVM        = "microsoft.compute/virtualmachines"
	azureTypeVNet      = "microsoft.network/virtualnetworks"
	azureTypeSubnet    = "microsoft.network/subnets"
	azureTypeSubnetN   = "microsoft.network/virtualnetworks/subnets"
	azureTypeNSG       = "microsoft.network/networksecuritygroups"
	azureTypeStorage   = "microsoft.storage/storageaccounts"
	azureTypeStoCnt    = "microsoft.storage/storageaccounts/blobservices/containers"
	azureTypeCosmos    = "microsoft.documentdb/databaseaccounts"
	azureTypeCosmosC   = "microsoft.documentdb/databaseaccounts/sqldatabases/containers"
	azureTypeWebSite   = "microsoft.web/sites"
	azureTypeDatabrick = "microsoft.databricks/workspaces"
)

// Portable service identifiers as emitted by the resourcediscovery walkers.
const (
	portableCompute    = "compute"
	portableNetworking = "networking"
	portableStorage    = "storage"
	portableDatabase   = "database"
	portableServerless = "serverless"
	portableDatabricks = "databricks"
)

// parsedKQL is the result of KQL parsing — an engine Query plus the limit
// extracted from `| limit N` / `| take N` (0 means no caller-specified limit).
//
// ForceEmpty is set when the parser detects a contradiction in chained
// where-clauses (e.g. two different type filters AND-ed together). Real KQL
// `where type == 'X' | where type == 'Y'` returns zero rows because a single
// resource cannot have two types; the engine matcher would otherwise OR-merge
// the two values via the Services slice, so we short-circuit at the handler.
type parsedKQL struct {
	Query      resourcediscovery.Query
	Limit      int
	ForceEmpty bool

	// internal tracking — used by applyType / addTag to detect conflicts.
	typeSet     bool
	tagFirstVal map[string]string
}

// Pre-compiled KQL predicate regexes. Compiled once at package load; safe
// for concurrent use.
//

var (
	// type == 'X' / type =~ 'X' / type == "X".
	reWhereType = regexp.MustCompile(`(?i)^\s*type\s*(==|=~)\s*['"]([^'"]+)['"]\s*$`)
	// type in ('a','b') / type in~ ('a', "b") — case-insensitive in-list.
	reWhereTypeIn = regexp.MustCompile(`(?i)^\s*type\s+in~?\s*\(([^)]*)\)\s*$`)
	// a single quoted item inside an in-list.
	reQuotedItem = regexp.MustCompile(`['"]([^'"]+)['"]`)
	// resourceGroup == 'X'.
	reWhereRG = regexp.MustCompile(`(?i)^\s*resourceGroup\s*==\s*['"]([^'"]+)['"]\s*$`)
	// location == 'X'.
	reWhereLocation = regexp.MustCompile(`(?i)^\s*location\s*==\s*['"]([^'"]+)['"]\s*$`)
	// tags['k'] == 'v' / tags["k"] == "v".
	reWhereTagsBracket = regexp.MustCompile(`(?i)^\s*tags\s*\[\s*['"]([^'"]+)['"]\s*\]\s*==\s*['"]([^'"]+)['"]\s*$`)
	// tags.k == 'v'.
	reWhereTagsDot = regexp.MustCompile(`(?i)^\s*tags\.([A-Za-z0-9_-]+)\s*==\s*['"]([^'"]+)['"]\s*$`)
	// limit N / take N.
	reLimit = regexp.MustCompile(`(?i)^\s*(limit|take)\s+(\d+)\s*$`)
)

// parseKQL splits the query on `|` and applies each clause to a Query under
// construction. Always returns a valid parsedKQL; unrecognized clauses
// (project, summarize, join, …) are silently ignored — the stub favors
// returning some result over a 400 on syntax we don't model yet.
func parseKQL(query string) parsedKQL {
	out := parsedKQL{}

	clauses := strings.Split(query, "|")
	for _, raw := range clauses {
		clause := strings.TrimSpace(raw)
		if clause == "" {
			continue
		}

		applyClause(&out, clause)
	}

	return out
}

func applyClause(out *parsedKQL, clause string) {
	// Drop the leading "Resources" table reference; it's the only one we
	// support and it carries no constraint.
	if strings.EqualFold(clause, "Resources") {
		return
	}

	if strings.HasPrefix(strings.ToLower(clause), "where ") {
		applyWhere(out, strings.TrimSpace(clause[len("where "):]))
		return
	}

	if m := reLimit.FindStringSubmatch(clause); m != nil {
		if n, err := strconv.Atoi(m[2]); err == nil {
			out.Limit = n
		}

		return
	}
}

// applyWhere routes a single "where" predicate to the matching field on the
// engine Query. Unknown predicates are tolerated and left untouched — see
// parseKQL's package-level comment for the rationale.
func applyWhere(out *parsedKQL, body string) {
	if m := reWhereType.FindStringSubmatch(body); m != nil {
		applyType(out, m[2])
		return
	}

	if m := reWhereTypeIn.FindStringSubmatch(body); m != nil {
		items := reQuotedItem.FindAllStringSubmatch(m[1], -1)

		types := make([]string, 0, len(items))
		for _, it := range items {
			types = append(types, it[1])
		}

		applyTypeList(out, types)

		return
	}

	if m := reWhereRG.FindStringSubmatch(body); m != nil {
		// The engine does not track resource groups; every Azure resource
		// is bucketed under a single "default" group. Filtering by RG is
		// therefore a no-op here — documented limitation; revisit if the
		// engine grows real RG awareness.
		_ = m
		return
	}

	if m := reWhereLocation.FindStringSubmatch(body); m != nil {
		out.Query.Region = m[1]
		return
	}

	if m := reWhereTagsBracket.FindStringSubmatch(body); m != nil {
		addTag(out, m[1], m[2])
		return
	}

	if m := reWhereTagsDot.FindStringSubmatch(body); m != nil {
		addTag(out, m[1], m[2])
		return
	}
}

// applyType maps an Azure type string to portable Service + Type. Lower-case
// before matching since Azure types are case-insensitive in KQL. A second
// type clause is treated as an AND contradiction — real KQL would yield
// zero rows because a resource cannot have two types — so ForceEmpty is
// flipped and later short-circuits the handler.
func applyType(out *parsedKQL, azureType string) {
	if out.typeSet {
		out.ForceEmpty = true
		return
	}

	out.typeSet = true

	svc, typ := mapAzureType(strings.ToLower(azureType))
	if svc != "" {
		out.Query.Services = []string{svc}
	}

	if typ != "" {
		out.Query.Type = typ
	}
}

// applyTypeList handles `where type in~ ('a', 'b')` — an any-of set of types.
// Each Azure type maps to a portable (service, type) pair; the services and
// types are unioned into the engine Query as any-of filters. Like applyType,
// a second type clause AND-ed on top is a contradiction and flips ForceEmpty.
func applyTypeList(out *parsedKQL, azureTypes []string) {
	if out.typeSet {
		out.ForceEmpty = true
		return
	}

	out.typeSet = true

	for _, at := range azureTypes {
		svc, typ := mapAzureType(strings.ToLower(strings.TrimSpace(at)))
		if svc != "" {
			out.Query.Services = appendUnique(out.Query.Services, svc)
		}

		if typ != "" {
			out.Query.Types = appendUnique(out.Query.Types, typ)
		}
	}
}

// appendUnique appends v to s only if it is not already present, preserving
// order. Keeps the any-of filter slices free of duplicate entries.
func appendUnique(s []string, v string) []string {
	for _, existing := range s {
		if existing == v {
			return s
		}
	}

	return append(s, v)
}

// addTag records a tag predicate. Repeating the same key with a different
// value is a KQL contradiction (a single tag cannot hold two values at
// once); flips ForceEmpty so the handler returns an empty result.
func addTag(out *parsedKQL, key, value string) {
	if prev, seen := out.tagFirstVal[key]; seen && prev != value {
		out.ForceEmpty = true
		return
	}

	if out.tagFirstVal == nil {
		out.tagFirstVal = make(map[string]string)
	}

	out.tagFirstVal[key] = value

	if out.Query.Tags == nil {
		out.Query.Tags = make(map[string]string)
	}

	out.Query.Tags[key] = value
}

// mapAzureType translates a fully-qualified Azure resource type to the
// portable (service, type) pair the engine uses. Returns ("", "") for
// unmapped types so the filter is treated as match-none rather than
// match-all.
func mapAzureType(azureType string) (service, typ string) {
	switch azureType {
	case azureTypeVM:
		return portableCompute, "Instance"
	case azureTypeVNet:
		return portableNetworking, "VPC"
	case azureTypeSubnet, azureTypeSubnetN:
		return portableNetworking, "Subnet"
	case azureTypeNSG:
		return portableNetworking, "SecurityGroup"
	case azureTypeStorage, azureTypeStoCnt:
		return portableStorage, "Bucket"
	case azureTypeCosmos, azureTypeCosmosC:
		return portableDatabase, "Table"
	case azureTypeWebSite:
		return portableServerless, "Function"
	case azureTypeDatabrick:
		return portableDatabricks, "Workspace"
	default:
		return "", ""
	}
}
