package identity

import (
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/iam/driver"
)

// Policy verbs, weakest to strongest. A stronger verb subsumes the weaker ones.
const (
	verbInspect = "inspect"
	verbRead    = "read"
	verbUse     = "use"
	verbManage  = "manage"
)

// Verb ranks, so a stronger verb subsumes the weaker ones.
const (
	rankUnknown = iota
	rankInspect
	rankRead
	rankUse
	rankManage
)

// Statement subject kinds.
const (
	subjectAnyUser      = "any-user"
	subjectGroup        = "group"
	subjectDynamicGroup = "dynamic-group"
	subjectService      = "service"
)

// Statement locations and grammar keywords.
const (
	locationTenancy     = "tenancy"
	locationCompartment = "compartment"
	allResources        = "all-resources"
	keywordID           = "id"
	keywordTo           = "to"
	keywordIn           = "in"
	keywordWhere        = "where"
)

// Statement effects. Only allow grants; the cross-tenancy effects are stored
// verbatim and never match a request inside this tenancy.
const (
	effectAllow   = "allow"
	effectEndorse = "endorse"
	effectAdmit   = "admit"
	effectDefine  = "define"
)

// statement is one parsed OCI policy statement, e.g.
// "Allow group Admins to manage all-resources in compartment dev".
type statement struct {
	Effect       string
	SubjectKind  string
	Subjects     []string
	SubjectByID  bool
	Verb         string
	ResourceType string
	LocationKind string
	Location     string
	LocationByID bool
	Condition    string
	Text         string
}

// parseStatement parses one English-like OCI policy statement.
func parseStatement(text string) (statement, error) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return statement{}, cerrors.New(cerrors.InvalidArgument, "policy statement is empty")
	}

	st := statement{Effect: strings.ToLower(fields[0]), Text: strings.Join(fields, " ")}

	switch st.Effect {
	case effectAllow:
	case effectEndorse, effectAdmit, effectDefine:
		return st, nil
	default:
		return statement{}, cerrors.Newf(cerrors.InvalidArgument,
			"policy statement %q must start with Allow, Endorse, Admit or Define", text)
	}

	rest, err := st.parseSubjectClause(fields[1:], text)
	if err != nil {
		return statement{}, err
	}

	rest, err = st.parseVerbClause(rest, text)
	if err != nil {
		return statement{}, err
	}

	if idx := indexFold(rest, keywordWhere); idx >= 0 {
		st.Condition = strings.Join(rest[idx+1:], " ")
		rest = rest[:idx]
	}

	if err := st.parseLocation(rest, text); err != nil {
		return statement{}, err
	}

	return st, nil
}

// parseSubjectClause consumes "<subject> to" and returns what follows.
func (s *statement) parseSubjectClause(fields []string, text string) ([]string, error) {
	toIdx := indexFold(fields, keywordTo)
	if toIdx <= 0 {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "policy statement %q: expected a subject followed by \"to\"", text)
	}

	tokens := fields[:toIdx]

	kind := strings.ToLower(tokens[0])
	switch kind {
	case subjectAnyUser:
		s.SubjectKind = subjectAnyUser
		return fields[toIdx+1:], nil
	case subjectGroup, subjectDynamicGroup, subjectService:
		s.SubjectKind = kind
	default:
		return nil, cerrors.Newf(cerrors.InvalidArgument, "policy statement %q: unknown subject %q", text, tokens[0])
	}

	tokens = tokens[1:]
	if len(tokens) > 0 && strings.EqualFold(tokens[0], keywordID) {
		s.SubjectByID = true
		tokens = tokens[1:]
	}

	s.Subjects = splitList(tokens)
	if len(s.Subjects) == 0 {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "policy statement %q: subject %q names nothing", text, kind)
	}

	return fields[toIdx+1:], nil
}

// parseVerbClause consumes "<verb> <resource-type> in" and returns what follows.
func (s *statement) parseVerbClause(fields []string, text string) ([]string, error) {
	if len(fields) == 0 {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "policy statement %q: expected a verb after \"to\"", text)
	}

	s.Verb = strings.ToLower(fields[0])
	if verbRank(s.Verb) == rankUnknown {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"policy statement %q: unknown verb %q, want inspect, read, use or manage", text, fields[0])
	}

	fields = fields[1:]

	inIdx := indexFold(fields, keywordIn)
	if inIdx < 1 {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"policy statement %q: expected a resource type followed by \"in\"", text)
	}

	s.ResourceType = strings.ToLower(strings.Join(fields[:inIdx], " "))

	return fields[inIdx+1:], nil
}

// parseLocation parses the clause after "in".
func (s *statement) parseLocation(tokens []string, text string) error {
	if len(tokens) == 0 {
		return cerrors.Newf(cerrors.InvalidArgument, "policy statement %q: expected a location after \"in\"", text)
	}

	switch strings.ToLower(tokens[0]) {
	case locationTenancy:
		s.LocationKind = locationTenancy
		return nil
	case locationCompartment:
		s.LocationKind = locationCompartment
	default:
		return cerrors.Newf(cerrors.InvalidArgument,
			"policy statement %q: expected \"in tenancy\" or \"in compartment ...\"", text)
	}

	tokens = tokens[1:]
	if len(tokens) > 0 && strings.EqualFold(tokens[0], keywordID) {
		s.LocationByID = true
		tokens = tokens[1:]
	}

	if len(tokens) == 0 {
		return cerrors.Newf(cerrors.InvalidArgument, "policy statement %q: names no compartment", text)
	}

	// A nested path is written "parent:child", unspaced. Keeping only the first
	// token would drop the rest and grant the parent's whole subtree instead.
	if len(tokens) > 1 {
		return cerrors.Newf(cerrors.InvalidArgument,
			"policy statement %q: compartment %q must be one token; write a nested path as parent%schild",
			text, strings.Join(tokens, " "), pathSeparator)
	}

	s.Location = tokens[0]

	return nil
}

// grantsSubject reports whether the statement's subject covers the requester.
func (s *statement) grantsSubject(req *driver.AccessRequest) bool {
	switch s.SubjectKind {
	case subjectAnyUser:
		return req.AnyUser
	case subjectGroup:
		return intersectsFold(s.Subjects, req.Groups)
	case subjectDynamicGroup:
		return intersectsFold(s.Subjects, req.DynamicGroups)
	default:
		return false
	}
}

// grantsAccess reports whether the statement's verb and resource type cover the
// request. The location is resolved separately, against the compartment tree.
func (s *statement) grantsAccess(req *driver.AccessRequest) bool {
	if s.Effect != effectAllow {
		return false
	}

	if !s.grantsSubject(req) {
		return false
	}

	if verbRank(s.Verb) < verbRank(strings.ToLower(req.Verb)) {
		return false
	}

	return coversResourceType(s.ResourceType, strings.ToLower(req.ResourceType))
}

// verbRank orders the policy verbs; an unknown verb ranks lowest.
func verbRank(verb string) int {
	switch verb {
	case verbInspect:
		return rankInspect
	case verbRead:
		return rankRead
	case verbUse:
		return rankUse
	case verbManage:
		return rankManage
	default:
		return rankUnknown
	}
}

// resourceFamilies maps an OCI resource family onto the types it covers.
func resourceFamilies() map[string][]string {
	return map[string][]string{
		"object-family":          {"buckets", "objects", "object-versions"},
		"instance-family":        {"instances", "instance-images", "console-histories"},
		"virtual-network-family": {"vcns", "subnets", "route-tables", "security-lists", "internet-gateways", "nat-gateways"},
		"database-family":        {"db-systems", "db-homes", "databases", "autonomous-databases"},
		"file-family":            {"file-systems", "mount-targets", "export-sets"},
		"cluster-family":         {"clusters", "cluster-node-pools"},
		"volume-family":          {"volumes", "volume-attachments", "volume-backups"},
	}
}

// coversResourceType reports whether a statement's resource type covers want.
func coversResourceType(stmtType, want string) bool {
	if stmtType == allResources || stmtType == want {
		return true
	}

	for _, member := range resourceFamilies()[stmtType] {
		if member == want {
			return true
		}
	}

	return false
}

// indexFold returns the index of the first token equal to keyword, ignoring case.
func indexFold(tokens []string, keyword string) int {
	for i, tok := range tokens {
		if strings.EqualFold(tok, keyword) {
			return i
		}
	}

	return -1
}

// splitList splits a comma-separated name list that may also be space-separated.
func splitList(tokens []string) []string {
	var out []string

	for _, part := range strings.Split(strings.Join(tokens, " "), ",") {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, name)
		}
	}

	return out
}

// intersectsFold reports whether the two lists share an entry, ignoring case.
func intersectsFold(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if strings.EqualFold(x, y) {
				return true
			}
		}
	}

	return false
}
