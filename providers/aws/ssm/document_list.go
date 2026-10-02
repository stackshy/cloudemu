package ssm

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	ssmdriver "github.com/stackshy/cloudemu/v2/providers/aws/ssm/driver"
)

// Sharing limits from the ModifyDocumentPermission reference.
const (
	maxSharesPerCall = 20
	maxShares        = 1000
	shareAll         = "all"

	filterKeyName          = "Name"
	filterKeyOwner         = "Owner"
	filterKeyPlatformTypes = "PlatformTypes"
	filterKeyDocumentType  = "DocumentType"
	filterKeyTargetType    = "TargetType"
	filterKeySearchKeyword = "SearchKeyword"
)

var (
	shareAccountPattern = regexp.MustCompile(`^(?i:all|\d{12})$`)
	sharedVersionRegexp = regexp.MustCompile(`^([$]LATEST|[$]DEFAULT|[$]ALL)$`)
)

// ListDocuments returns the default version of every document that matches
// all filters, customer and AWS-owned alike, sorted by name.
func (m *Mock) ListDocuments(_ context.Context, filters []ssmdriver.DocumentFilter) ([]ssmdriver.DocumentDescription, error) {
	for _, f := range filters {
		if !knownFilterKey(f.Key) {
			return nil, ssmErrf(excInvalidFilterKey, errors.InvalidArgument, "The filter key %s is not valid.", f.Key)
		}
	}

	m.docMu.RLock()
	defer m.docMu.RUnlock()

	all := make([]*document, 0, m.documents.Len()+len(m.catalog))
	all = append(all, m.documents.SortedValues()...)

	for _, d := range m.catalog {
		all = append(all, d)
	}

	var out []ssmdriver.DocumentDescription

	for _, d := range all {
		if m.matchesAll(d, filters) {
			out = append(out, *d.describe(d.versions[d.defaultVersion]))
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}

func knownFilterKey(key string) bool {
	switch key {
	case filterKeyName, filterKeyOwner, filterKeyPlatformTypes, filterKeyDocumentType, filterKeyTargetType,
		filterKeySearchKeyword:
		return true
	default:
		return strings.HasPrefix(key, "tag:") && len(key) > len("tag:")
	}
}

func (m *Mock) matchesAll(d *document, filters []ssmdriver.DocumentFilter) bool {
	for _, f := range filters {
		if len(f.Values) > 0 && !m.matches(d, f) {
			return false
		}
	}

	return true
}

// matches reports whether any value of f selects d.
func (m *Mock) matches(d *document, f ssmdriver.DocumentFilter) bool {
	v := d.versions[d.defaultVersion]

	for _, want := range f.Values {
		var ok bool

		switch f.Key {
		case filterKeyName:
			ok = strings.HasPrefix(d.name, want)
		case filterKeySearchKeyword:
			ok = strings.Contains(strings.ToLower(d.name), strings.ToLower(want))
		case filterKeyOwner:
			ok = m.ownerMatches(d, want)
		case filterKeyPlatformTypes:
			ok = containsFold(v.meta.platformTypes, want)
		case filterKeyDocumentType:
			ok = d.docType == want
		case filterKeyTargetType:
			ok = v.targetType == want
		default:
			tv, has := d.tags[strings.TrimPrefix(f.Key, "tag:")]
			ok = has && tv == want
		}

		if ok {
			return true
		}
	}

	return false
}

// ownerMatches applies an Owner filter value. Nothing is shared into this
// single emulated account, so ThirdParty never matches and Private is the
// same as Self.
func (m *Mock) ownerMatches(d *document, owner string) bool {
	switch owner {
	case "Self", "Private":
		return d.owner == m.opts.AccountID
	case ssmdriver.OwnerAmazon:
		return d.owner == ssmdriver.OwnerAmazon
	case "Public":
		_, ok := d.shares[shareAll]
		return ok
	case "All":
		return true
	default:
		return false
	}
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}

	return false
}

// DescribeDocumentPermission lists the accounts a document is shared with.
func (m *Mock) DescribeDocumentPermission(_ context.Context, name, permissionType string) ([]ssmdriver.AccountSharingInfo, error) {
	if err := checkPermissionType(permissionType); err != nil {
		return nil, err
	}

	m.docMu.RLock()
	defer m.docMu.RUnlock()

	d, err := m.lookupDocument(name)
	if err != nil {
		return nil, err
	}

	out := make([]ssmdriver.AccountSharingInfo, 0, len(d.shares))
	for acct, ver := range d.shares {
		out = append(out, ssmdriver.AccountSharingInfo{AccountID: acct, SharedDocumentVersion: ver})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].AccountID < out[j].AccountID })

	return out, nil
}

func checkPermissionType(t string) error {
	if t != ssmdriver.PermissionTypeShare {
		return ssmErrf(excInvalidPermissionType, errors.InvalidArgument,
			"The permission type isn't supported. Share is the only supported permission type.")
	}

	return nil
}

// ModifyDocumentPermission shares a document with accounts or publicly
// ("All"), or stops sharing it. Removal wins over an add of the same id.
func (m *Mock) ModifyDocumentPermission(_ context.Context, in *ssmdriver.ModifyPermissionInput) error {
	if err := checkModifyPermission(in); err != nil {
		return err
	}

	shared := in.SharedDocumentVersion
	if shared == "" {
		shared = versionDefault
	}

	m.docMu.Lock()
	defer m.docMu.Unlock()

	d, err := m.ownedDocument(in.Name)
	if err != nil {
		return err
	}

	next := make(map[string]string, len(d.shares)+len(in.AccountIDsToAdd))
	for k, v := range d.shares {
		next[k] = v
	}

	for _, id := range in.AccountIDsToAdd {
		next[strings.ToLower(id)] = shared
	}

	for _, id := range in.AccountIDsToRemove {
		delete(next, strings.ToLower(id))
	}

	if len(next) > maxShares {
		return ssmErrf(excDocumentPermissionLimit, errors.ResourceExhausted,
			"The document can't be shared with more than %d accounts.", maxShares)
	}

	d.shares = next
	if len(next) == 0 {
		d.shares = nil
	}

	return nil
}

func checkModifyPermission(in *ssmdriver.ModifyPermissionInput) error {
	if err := checkPermissionType(in.PermissionType); err != nil {
		return err
	}

	if len(in.AccountIDsToAdd) == 0 && len(in.AccountIDsToRemove) == 0 {
		return ssmErrf(excValidation, errors.InvalidArgument,
			"You must specify a value for AccountIdsToAdd or AccountIdsToRemove.")
	}

	if len(in.AccountIDsToAdd) > maxSharesPerCall || len(in.AccountIDsToRemove) > maxSharesPerCall {
		return ssmErrf(excDocumentPermissionLimit, errors.ResourceExhausted,
			"You can specify a maximum of %d accounts per API operation to share a private document.", maxSharesPerCall)
	}

	for _, id := range append(append([]string{}, in.AccountIDsToAdd...), in.AccountIDsToRemove...) {
		if !shareAccountPattern.MatchString(id) {
			return ssmErrf(excValidation, errors.InvalidArgument,
				"1 validation error detected: Value '%s' at 'accountIds' failed to satisfy constraint: "+
					"Member must satisfy regular expression pattern: (?i)all|[0-9]{12}", id)
		}
	}

	if in.SharedDocumentVersion != "" && !sharedVersionRegexp.MatchString(in.SharedDocumentVersion) {
		return ssmErrf(excValidation, errors.InvalidArgument,
			"1 validation error detected: Value '%s' at 'sharedDocumentVersion' failed to satisfy constraint: "+
				"Member must satisfy regular expression pattern: ([$]LATEST|[$]DEFAULT|[$]ALL)", in.SharedDocumentVersion)
	}

	return nil
}

// TagDocument adds or overwrites tags on a customer document.
func (m *Mock) TagDocument(_ context.Context, name string, tags map[string]string) error {
	m.docMu.Lock()
	defer m.docMu.Unlock()

	d, ok := m.documents.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "document %q not found", name)
	}

	if d.tags == nil {
		d.tags = make(map[string]string, len(tags))
	}

	for k, v := range tags {
		d.tags[k] = v
	}

	return nil
}

// UntagDocument removes tags by key from a customer document.
func (m *Mock) UntagDocument(_ context.Context, name string, keys []string) error {
	m.docMu.Lock()
	defer m.docMu.Unlock()

	d, ok := m.documents.Get(name)
	if !ok {
		return errors.Newf(errors.NotFound, "document %q not found", name)
	}

	for _, k := range keys {
		delete(d.tags, k)
	}

	return nil
}

// ListDocumentTags returns a customer document's tags.
func (m *Mock) ListDocumentTags(_ context.Context, name string) (map[string]string, error) {
	m.docMu.RLock()
	defer m.docMu.RUnlock()

	d, ok := m.documents.Get(name)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "document %q not found", name)
	}

	return copyTags(d.tags), nil
}
