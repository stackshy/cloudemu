package aws_test

// REST-JSON top-level path-prefix collision test (Architecture Theme 2, #590).
//
// Every AWS REST-JSON handler claims traffic by the first (top-level) segment
// of the request path — either a dated API-version segment (Route 53's
// "2013-04-01", EFS's "2015-02-01", …) or a bare resource root (GuardDuty's
// "detector", EKS's "clusters", …). Because server.Server is first-match-wins,
// two handlers that claim the SAME top-level segment collide: whichever
// registers first shadows the other, silently, with no compile or test error.
//
// restJSONTopLevelPrefixes is the curated registry of the top-level segments
// each REST-JSON handler owns, derived from each package's Matches(). The test
// asserts no segment is claimed by more than one handler, EXCEPT the segments
// in sharedTopLevelPrefixes, which are intentionally multi-claimed and
// disambiguated by deeper matching (ARN shape, path suffix). A future handler
// that reuses a reserved segment without declaring it shared turns a silent
// shadow into a test failure.
//
// This registry must be kept in sync when a REST-JSON handler is added or its
// claimed roots change; that upkeep is the point — it forces the collision
// question to be answered explicitly.

import (
	"sort"
	"testing"
)

// restJSONTopLevelPrefixes maps each AWS REST-JSON handler to the top-level
// path segments it claims. The S3 handler is the deliberate catch-all fallback
// and owns no reserved prefix, so it is excluded.
//
//nolint:gochecknoglobals // test fixture: the reserved-prefix registry under test.
var restJSONTopLevelPrefixes = map[string][]string{
	// Dated / versioned API-prefix handlers: the top-level segment is the
	// version, unique per service.
	"efs":        {"2015-02-01"},
	"opensearch": {"2021-01-01"},
	"route53":    {"2013-04-01"},
	"lambda":     {"2015-03-31", "2017-03-31", "2018-10-31"},
	"sesv2":      {"v2"},
	"kafka":      {"v1", "api", "replication"},

	// Bare-root REST handlers.
	"eks":                 {"clusters", "tags"},
	"bedrockagent":        {"agents", "knowledgebases", "flows", "prompts"},
	"bedrockagentruntime": {"agents", "knowledgebases"},
	"sagemakerruntime":    {"endpoints"},
	"k8s":                 {"k8s"},
	"guardduty": {
		"detector", "admin", "invitation", "tags", "malware-scan", "malware-scans",
		"malware-protection-plan", "object-malware-scan", "organization",
	},
	"bedrock": {
		"foundation-models", "model-customization-jobs", "custom-models", "model",
		"guardrails", "provisioned-model-throughput", "async-invoke", "inference-profiles",
	},
	"vpclattice": {
		"servicenetworks", "services", "targetgroups", "servicenetworkvpcassociations",
		"servicenetworkvpcendpointassociations", "servicenetworkserviceassociations",
		"servicenetworkresourceassociations", "resourceconfigurations", "resourcegateways",
		"resourceendpointassociations", "accesslogsubscriptions", "authpolicy",
		"resourcepolicy", "domainverifications", "tags",
	},
	"resourceexplorer2": {
		"CreateView", "DeleteView", "ListViews", "GetView", "Search", "ListResources",
		"ListIndexes", "GetIndex", "CreateIndex", "GetDefaultView",
	},
}

// sharedTopLevelPrefixes are the top-level segments intentionally claimed by
// more than one handler and safely disambiguated by deeper matching. Each
// entry lists the exact set of handlers permitted to share it.
//
//nolint:gochecknoglobals // test fixture: the documented shared-prefix allowlist.
var sharedTopLevelPrefixes = map[string]map[string]bool{
	// The generic /tags/{ResourceArn} REST surface is shared: each handler
	// claims it only for ARNs it owns, so tag requests fall through to the
	// owning service.
	"tags": {"eks": true, "guardduty": true, "vpclattice": true},
	// bedrock-agent-runtime shares the /agents and /knowledgebases roots with
	// the bedrock-agent control plane, matching only the runtime suffixes; it
	// registers first so its more-specific Matches wins.
	"agents":         {"bedrockagent": true, "bedrockagentruntime": true},
	"knowledgebases": {"bedrockagent": true, "bedrockagentruntime": true},
}

// TestRESTJSONPrefixesAreDisjoint asserts no top-level path segment is claimed
// by two REST-JSON handlers unless that segment is an allowlisted shared root
// claimed only by its documented owners.
func TestRESTJSONPrefixesAreDisjoint(t *testing.T) {
	// segment -> set of handlers claiming it.
	claimants := map[string]map[string]bool{}

	for handler, segs := range restJSONTopLevelPrefixes {
		for _, seg := range segs {
			if claimants[seg] == nil {
				claimants[seg] = map[string]bool{}
			}

			claimants[seg][handler] = true
		}
	}

	for _, seg := range sortedKeys(claimants) {
		owners := claimants[seg]
		if len(owners) < 2 {
			continue
		}

		allowed, isShared := sharedTopLevelPrefixes[seg]
		if !isShared {
			t.Errorf("top-level prefix %q is claimed by multiple handlers %v but is not an allowlisted shared root; "+
				"this is a silent routing shadow — resolve the collision or document it in sharedTopLevelPrefixes",
				seg, sortedSet(owners))

			continue
		}

		for owner := range owners {
			if !allowed[owner] {
				t.Errorf("handler %q claims shared prefix %q but is not in its allowlist %v; "+
					"first-match-wins may shadow it — verify Matches() disambiguation and update the allowlist",
					owner, seg, sortedSet(allowed))
			}
		}
	}
}

// TestSharedPrefixAllowlistIsGrounded guards against stale allowlist entries:
// every shared prefix (and each of its declared owners) must actually appear in
// the reserved-prefix registry.
func TestSharedPrefixAllowlistIsGrounded(t *testing.T) {
	for seg, owners := range sharedTopLevelPrefixes {
		for owner := range owners {
			segs, ok := restJSONTopLevelPrefixes[owner]
			if !ok {
				t.Errorf("allowlist owner %q for shared prefix %q is not a registered REST-JSON handler", owner, seg)

				continue
			}

			if !contains(segs, seg) {
				t.Errorf("allowlist says handler %q shares prefix %q, but %q does not claim it in the registry", owner, seg, owner)
			}
		}
	}
}

func sortedKeys(m map[string]map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

func sortedSet(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}

	return false
}
