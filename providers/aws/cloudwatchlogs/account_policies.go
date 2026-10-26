package cloudwatchlogs

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatchlogs/dataprotection"
	"github.com/stackshy/cloudemu/v2/providers/aws/cloudwatchlogs/filterpattern"
	"github.com/stackshy/cloudemu/v2/services/logging/driver"
)

// Account policy limits, per the PutAccountPolicy reference.
const (
	maxSelectionCriteriaBytes = 25 * 1024
	maxCriteriaValues         = 50
	maxScopedTransformer      = 20
	maxScopedFieldIndex       = 20
	maxScopedMetricExtraction = 5
	reservedPolicyPrefix      = "aws/"
)

// Selection criteria keys and operators.
const (
	critLogGroupName   = "LogGroupName"
	critLogGroupPrefix = "LogGroupNamePrefix"
	critDataSourceName = "DataSourceName"
	critDataSourceType = "DataSourceType"
	opIn               = "IN"
	opNotIn            = "NOT IN"
	opEquals           = "="
)

var (
	criterionRE   = regexp.MustCompile(`^\s*(LogGroupNamePrefix|LogGroupName|DataSourceName|DataSourceType)\s*(NOT\s+IN|IN|=)\s*(.+?)\s*$`)
	criteriaAndRE = regexp.MustCompile(`\s+AND\s+`)
	accountIDRE   = regexp.MustCompile(`^\d{12}$`)
	spaceRunRE    = regexp.MustCompile(`\s+`)
)

// accountPolicy is a stored account policy with the parsed forms ingest uses.
type accountPolicy struct {
	info     driver.AccountPolicy
	criteria []criterion
	dp       *dataprotection.Policy
	sub      *driver.SubscriptionFilterInfo
}

type criterion struct {
	key, op string
	values  []string
}

func accountPolicyKey(policyType, name string) string { return policyType + "\x00" + name }

func validPolicyType(t string) bool {
	switch t {
	case driver.AccountPolicyDataProtection, driver.AccountPolicySubscriptionFilter, driver.AccountPolicyFieldIndex,
		driver.AccountPolicyTransformer, driver.AccountPolicyMetricExtraction:
		return true
	default:
		return false
	}
}

func policyTypeError(t string) error {
	return errors.Newf(errors.InvalidArgument,
		"1 validation error detected: Value '%s' at 'policyType' failed to satisfy constraint: Member must satisfy enum value set: "+
			"[DATA_PROTECTION_POLICY, SUBSCRIPTION_FILTER_POLICY, FIELD_INDEX_POLICY, TRANSFORMER_POLICY, METRIC_EXTRACTION_POLICY]", t)
}

// PutAccountPolicy creates or replaces an account policy, identified by its
// type and name.
func (m *Mock) PutAccountPolicy(_ context.Context, in *driver.PutAccountPolicyInput) (*driver.AccountPolicy, error) {
	ap, err := m.buildAccountPolicy(in, true)
	if err != nil {
		return nil, err
	}

	ap.info.LastUpdatedTime = m.opts.Clock.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkAccountPolicyQuota(ap); err != nil {
		return nil, err
	}

	if m.accountPolicies == nil {
		m.accountPolicies = map[string]*accountPolicy{}
	}

	m.accountPolicies[accountPolicyKey(ap.info.PolicyType, ap.info.PolicyName)] = ap

	out := ap.info

	return &out, nil
}

// buildAccountPolicy validates a request and parses its document. checkRefs
// also checks that referenced resources exist; restore skips it.
func (m *Mock) buildAccountPolicy(in *driver.PutAccountPolicyInput, checkRefs bool) (*accountPolicy, error) {
	if err := validateAccountPolicyInput(in); err != nil {
		return nil, err
	}

	crits, err := parseSelectionCriteria(in.PolicyType, in.SelectionCriteria)
	if err != nil {
		return nil, err
	}

	ap := &accountPolicy{
		info: driver.AccountPolicy{
			AccountID: m.opts.AccountID, PolicyName: in.PolicyName, PolicyType: in.PolicyType,
			PolicyDocument: in.PolicyDocument, Scope: driver.AccountPolicyScopeAll, SelectionCriteria: in.SelectionCriteria,
		},
		criteria: crits,
	}

	if err := m.parseAccountDocument(ap, checkRefs); err != nil {
		return nil, err
	}

	return ap, nil
}

func validateAccountPolicyInput(in *driver.PutAccountPolicyInput) error {
	switch {
	case in.PolicyName == "":
		return errors.New(errors.InvalidArgument, "policyName is required")
	case strings.HasPrefix(in.PolicyName, reservedPolicyPrefix):
		return errors.New(errors.InvalidArgument, "Policy names can't start with aws/")
	case !validPolicyType(in.PolicyType):
		return policyTypeError(in.PolicyType)
	case in.Scope != "" && in.Scope != driver.AccountPolicyScopeAll:
		return errors.Newf(errors.InvalidArgument,
			"1 validation error detected: Value '%s' at 'scope' failed to satisfy constraint: Member must satisfy enum value set: [ALL]",
			in.Scope)
	case in.PolicyDocument == "":
		return errors.New(errors.InvalidArgument, "policyDocument is required")
	case len(in.SelectionCriteria) > maxSelectionCriteriaBytes:
		return errors.New(errors.InvalidArgument, "selectionCriteria must be at most 25KB")
	}

	return nil
}

func (m *Mock) parseAccountDocument(ap *accountPolicy, checkRefs bool) error {
	doc := ap.info.PolicyDocument

	switch ap.info.PolicyType {
	case driver.AccountPolicyDataProtection:
		var err error
		if checkRefs {
			ap.dp, err = m.parseDataProtection(doc)
		} else {
			ap.dp, err = dataprotection.Parse(doc)
		}

		return err
	case driver.AccountPolicySubscriptionFilter:
		sub, err := parseSubscriptionPolicy(doc)
		ap.sub = sub

		return err
	case driver.AccountPolicyFieldIndex:
		return validateFieldIndexPolicy(doc)
	default:
		if !json.Valid([]byte(doc)) {
			return errors.New(errors.InvalidArgument, "policyDocument is not valid JSON")
		}

		return nil
	}
}

func parseSubscriptionPolicy(doc string) (*driver.SubscriptionFilterInfo, error) {
	var d struct {
		DestinationArn string `json:"DestinationArn"`
		RoleArn        string `json:"RoleArn"`
		FilterPattern  string `json:"FilterPattern"`
		Distribution   string `json:"Distribution"`
	}

	if err := json.Unmarshal([]byte(doc), &d); err != nil {
		return nil, errors.New(errors.InvalidArgument, "policyDocument is not a valid subscription filter policy")
	}

	if !strings.HasPrefix(d.DestinationArn, "arn:") {
		return nil, errors.New(errors.InvalidArgument, "The subscription filter policy must have a DestinationArn")
	}

	if d.Distribution != "" && d.Distribution != "Random" && d.Distribution != "ByLogStream" {
		return nil, errors.Newf(errors.InvalidArgument, "Distribution %s must be Random or ByLogStream", d.Distribution)
	}

	if _, err := filterpattern.Parse(d.FilterPattern); err != nil {
		return nil, err
	}

	return &driver.SubscriptionFilterInfo{
		FilterPattern: d.FilterPattern, DestinationARN: d.DestinationArn, RoleARN: d.RoleArn, Distribution: d.Distribution,
	}, nil
}

func validateFieldIndexPolicy(doc string) error {
	var d struct {
		Fields   []string `json:"Fields"`
		FieldsV2 map[string]struct {
			Type string `json:"type"`
		} `json:"FieldsV2"`
	}

	if err := json.Unmarshal([]byte(doc), &d); err != nil {
		return errors.New(errors.InvalidArgument, "policyDocument is not a valid field index policy")
	}

	if len(d.Fields)+len(d.FieldsV2) == 0 {
		return errors.New(errors.InvalidArgument, "A field index policy must contain at least one field index")
	}

	for _, f := range d.Fields {
		if _, dup := d.FieldsV2[f]; dup || f == "" {
			return errors.Newf(errors.InvalidArgument, "Field %q must appear once, in Fields or FieldsV2", f)
		}
	}

	for name, f := range d.FieldsV2 {
		if f.Type != "FIELD_INDEX" && f.Type != "FACET" {
			return errors.Newf(errors.InvalidArgument, "FieldsV2 %q type must be FIELD_INDEX or FACET", name)
		}
	}

	return nil
}

// parseSelectionCriteria parses clauses such as `LogGroupName NOT IN ["a"]`
// or `LogGroupNamePrefix = "test"`, joined with AND, and checks them against
// what the policy type supports.
func parseSelectionCriteria(policyType, s string) ([]criterion, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}

	if policyType == driver.AccountPolicyDataProtection {
		return nil, errors.New(errors.InvalidArgument, "selectionCriteria is not supported for DATA_PROTECTION_POLICY")
	}

	var out []criterion

	for _, clause := range criteriaAndRE.Split(s, -1) {
		c, err := parseCriterion(clause)
		if err != nil {
			return nil, err
		}

		out = append(out, c)
	}

	if !criteriaAllowed(policyType, out) {
		return nil, errors.Newf(errors.InvalidArgument, "selectionCriteria %q is not supported for %s", s, policyType)
	}

	return out, nil
}

func parseCriterion(clause string) (criterion, error) {
	bad := errors.Newf(errors.InvalidArgument, "Invalid selectionCriteria: %s", clause)

	m := criterionRE.FindStringSubmatch(clause)
	if m == nil {
		return criterion{}, bad
	}

	c := criterion{key: m[1], op: spaceRunRE.ReplaceAllString(m[2], " ")}

	if strings.HasPrefix(m[3], "[") {
		if err := json.Unmarshal([]byte(m[3]), &c.values); err != nil || len(c.values) == 0 {
			return criterion{}, bad
		}
	} else {
		var v string
		if err := json.Unmarshal([]byte(m[3]), &v); err != nil {
			return criterion{}, bad
		}

		c.values = []string{v}
	}

	if len(c.values) > maxCriteriaValues {
		return criterion{}, errors.Newf(errors.InvalidArgument, "selectionCriteria lists can have at most %d values", maxCriteriaValues)
	}

	return c, nil
}

func criteriaAllowed(policyType string, cs []criterion) bool {
	switch policyType {
	case driver.AccountPolicySubscriptionFilter:
		return isSingle(cs, critLogGroupName) && cs[0].op == opNotIn
	case driver.AccountPolicyTransformer:
		return isSingle(cs, critLogGroupPrefix) && cs[0].positive()
	case driver.AccountPolicyFieldIndex:
		return fieldIndexCriteriaAllowed(cs)
	default: // METRIC_EXTRACTION_POLICY
		return (isSingle(cs, critLogGroupName) || isSingle(cs, critLogGroupPrefix)) && cs[0].op != opEquals
	}
}

// positive reports an IN or = criterion.
func (c *criterion) positive() bool { return c.op == opIn || c.op == opEquals }

// isSingle reports criteria made of one clause on key.
func isSingle(cs []criterion, key string) bool { return len(cs) == 1 && cs[0].key == key }

// fieldIndexCriteriaAllowed accepts a LogGroupNamePrefix clause, or a
// DataSourceName clause AND a DataSourceType clause.
func fieldIndexCriteriaAllowed(cs []criterion) bool {
	if isSingle(cs, critLogGroupPrefix) {
		return cs[0].positive()
	}

	const dataSourceClauses = 2

	return len(cs) == dataSourceClauses && cs[0].key == critDataSourceName && cs[1].key == critDataSourceType &&
		cs[0].positive() && cs[1].positive()
}

// criteriaKind groups policies that share a quota: "" for no criteria, or
// the first criterion's key.
func criteriaKind(cs []criterion) string {
	if len(cs) == 0 {
		return ""
	}

	return cs[0].key
}

func quotaFor(policyType, kind string) int {
	switch {
	case policyType == driver.AccountPolicyDataProtection, policyType == driver.AccountPolicySubscriptionFilter, kind == "":
		return 1
	case policyType == driver.AccountPolicyTransformer:
		return maxScopedTransformer
	case policyType == driver.AccountPolicyFieldIndex:
		return maxScopedFieldIndex
	default:
		return maxScopedMetricExtraction
	}
}

// checkAccountPolicyQuota applies the per-type limits and the no-overlap
// rule. Caller holds m.mu.
func (m *Mock) checkAccountPolicyQuota(ap *accountPolicy) error {
	kind := criteriaKind(ap.criteria)
	limit := quotaFor(ap.info.PolicyType, kind)
	sameTypeOnly := ap.info.PolicyType == driver.AccountPolicyDataProtection ||
		ap.info.PolicyType == driver.AccountPolicySubscriptionFilter
	count := 0

	for _, other := range m.accountPolicies {
		if other.info.PolicyType != ap.info.PolicyType || other.info.PolicyName == ap.info.PolicyName {
			continue
		}

		if sameTypeOnly || criteriaKind(other.criteria) == kind {
			count++
		}

		if overlaps(ap.criteria, other.criteria) {
			return errors.Newf(errors.InvalidArgument,
				"The selectionCriteria of policy %s overlaps with the existing policy %s", ap.info.PolicyName, other.info.PolicyName)
		}
	}

	if count >= limit {
		return errors.Newf(errors.ResourceExhausted,
			"The account already has the maximum number of %s account policies", ap.info.PolicyType)
	}

	return nil
}

// overlaps reports whether two scoped policies of one type select the same
// log groups: prefix lists where one prefix starts with another, identical
// data sources, or two NOT IN policies.
func overlaps(a, b []criterion) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}

	// Only one NOT IN policy of a type is allowed, whatever its key.
	if a[0].op == opNotIn && b[0].op == opNotIn {
		return true
	}

	if a[0].key != b[0].key || a[0].op == opNotIn || b[0].op == opNotIn {
		return false
	}

	if a[0].key == critDataSourceName {
		return criteriaString(a) == criteriaString(b)
	}

	return prefixesOverlap(a[0].values, b[0].values)
}

// criteriaString renders criteria values for an equality check.
func criteriaString(cs []criterion) string {
	parts := make([]string, 0, len(cs))
	for i := range cs {
		parts = append(parts, strings.Join(cs[i].values, ","))
	}

	return strings.Join(parts, "/")
}

// prefixesOverlap reports whether one value of a starts with one of b, or the
// other way round.
func prefixesOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if strings.HasPrefix(x, y) || strings.HasPrefix(y, x) {
				return true
			}
		}
	}

	return false
}

// DescribeAccountPolicies lists the account's policies of one type, by name.
func (m *Mock) DescribeAccountPolicies(_ context.Context, in driver.DescribeAccountPoliciesInput) ([]driver.AccountPolicy, error) {
	if !validPolicyType(in.PolicyType) {
		return nil, policyTypeError(in.PolicyType)
	}

	if len(in.AccountIdentifiers) > 1 {
		return nil, errors.New(errors.InvalidArgument, "accountIdentifiers can have at most 1 item")
	}

	for _, id := range in.AccountIdentifiers {
		if !accountIDRE.MatchString(id) {
			return nil, errors.Newf(errors.InvalidArgument, "accountIdentifiers value %q must be a 12-digit account ID", id)
		}

		if id != m.opts.AccountID {
			return []driver.AccountPolicy{}, nil
		}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	out := []driver.AccountPolicy{}

	for _, ap := range m.accountPolicies {
		if ap.info.PolicyType == in.PolicyType && (in.PolicyName == "" || ap.info.PolicyName == in.PolicyName) {
			out = append(out, ap.info)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].PolicyName < out[j].PolicyName })

	return out, nil
}

// DeleteAccountPolicy removes an account policy. Events already masked by a
// data protection policy stay masked.
func (m *Mock) DeleteAccountPolicy(_ context.Context, name, policyType string) error {
	if !validPolicyType(policyType) {
		return policyTypeError(policyType)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := accountPolicyKey(policyType, name)
	if _, ok := m.accountPolicies[key]; !ok {
		return errors.Newf(errors.NotFound, "Policy with name [%s] does not exist", name)
	}

	delete(m.accountPolicies, key)

	return nil
}

// accountDataProtection is the account data protection policy, or nil.
func (m *Mock) accountDataProtection() *dataprotection.Policy {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, ap := range m.accountPolicies {
		if ap.dp != nil {
			return ap.dp
		}
	}

	return nil
}

// accountSubscription is the account subscription filter policy as a filter
// on group, or nil when there is none or the criteria exclude the group.
func (m *Mock) accountSubscription(group string) *driver.SubscriptionFilterInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, ap := range m.accountPolicies {
		if ap.sub == nil {
			continue
		}

		for _, c := range ap.criteria {
			for _, excluded := range c.values {
				if excluded == group {
					return nil
				}
			}
		}

		sf := *ap.sub
		sf.Name, sf.LogGroup, sf.CreatedAt = ap.info.PolicyName, group, ap.info.LastUpdatedTime

		return &sf
	}

	return nil
}
