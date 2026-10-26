package secretsmanager

import (
	"cmp"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/stackshy/cloudemu/v2/server/wire"
	secretsdriver "github.com/stackshy/cloudemu/v2/services/secrets/driver"
)

// ListSecrets / BatchGetSecretValue input enums, from the Secrets Manager API
// model (FilterNameStringType, SortByType, SortOrderType).
const (
	filterDescription   = "description"
	filterName          = "name"
	filterTagKey        = "tag-key"
	filterTagValue      = "tag-value"
	filterPrimaryRegion = "primary-region"
	filterOwningService = "owning-service"
	filterAll           = "all"

	sortByCreatedDate      = "created-date"
	sortByLastAccessedDate = "last-accessed-date"
	sortByLastChangedDate  = "last-changed-date"
	sortByName             = "name"

	sortOrderAsc  = "asc"
	sortOrderDesc = "desc"

	// listSecretsMaxResults is ListSecrets' documented MaxResults upper bound
	// (valid range 1-100).
	listSecretsMaxResults = 100
)

//nolint:gochecknoglobals // fixed enum value sets from the API model.
var (
	filterKeys = []string{
		filterDescription, filterName, filterTagKey, filterTagValue,
		filterPrimaryRegion, filterOwningService, filterAll,
	}
	sortByValues    = []string{sortByCreatedDate, sortByLastAccessedDate, sortByLastChangedDate, sortByName}
	sortOrderValues = []string{sortOrderAsc, sortOrderDesc}
)

// errValidation is the wire-level ValidationException ("The input fails to
// satisfy the constraints specified by the service", HTTP 400) Secrets Manager
// lists among its common errors, raised here for model constraint violations
// such as a value outside an enum.
type errValidation string

func (e errValidation) Error() string { return string(e) }

// enumViolation formats the model-constraint message for a value outside an
// enum, in the "1 validation error detected" shape AWS JSON services use.
func enumViolation(value, field string, allowed []string) errValidation {
	return errValidation(fmt.Sprintf(
		"1 validation error detected: Value '%s' at '%s' failed to satisfy constraint: "+
			"Member must satisfy enum value set: [%s]", value, field, strings.Join(allowed, ", ")))
}

// validateFilters rejects a filter Key outside FilterNameStringType, so a typo
// such as "Name" or "primary_region" fails instead of silently matching every
// secret.
func validateFilters(filters []secretFilter) error {
	for i := range filters {
		if !slices.Contains(filterKeys, filters[i].Key) {
			return enumViolation(filters[i].Key, fmt.Sprintf("filters.%d.member.key", i+1), filterKeys)
		}
	}

	return nil
}

// validateListSecrets checks the ListSecrets inputs that are constrained by the
// model: the filter keys, SortBy, SortOrder and the MaxResults range.
func validateListSecrets(req *listSecretsRequest) error {
	if err := validateFilters(req.Filters); err != nil {
		return err
	}

	if req.SortBy != "" && !slices.Contains(sortByValues, req.SortBy) {
		return enumViolation(req.SortBy, "sortBy", sortByValues)
	}

	if req.SortOrder != "" && !slices.Contains(sortOrderValues, req.SortOrder) {
		return enumViolation(req.SortOrder, "sortOrder", sortOrderValues)
	}

	if req.MaxResults != nil && (*req.MaxResults < 1 || *req.MaxResults > listSecretsMaxResults) {
		return errValidation(fmt.Sprintf(
			"1 validation error detected: Value '%d' at 'maxResults' failed to satisfy constraint: "+
				"Member must have value between 1 and %d", *req.MaxResults, listSecretsMaxResults))
	}

	return nil
}

// writeValidation writes a ValidationException.
func writeValidation(w http.ResponseWriter, err error) {
	wire.WriteJSONError(w, http.StatusBadRequest, "ValidationException", err.Error())
}

// secretOrder returns the strict total order a ListSecrets result is listed in.
// With neither SortBy nor SortOrder supplied it is name ascending (the order
// cloudemu has always used; AWS documents CreatedDate as the default key but
// not its direction). Otherwise SortBy picks the key (CreatedDate when omitted,
// as AWS documents) and SortOrder the direction (ascending unless "desc").
// Ties on the key fall back to name ascending, and names are unique, so the
// order is total and a cursor NextToken can resume after any entry.
func secretOrder(sortBy, sortOrder string) func(a, b *secretsdriver.SecretInfo) int {
	if sortBy == "" && sortOrder == "" {
		sortBy = sortByName
	}

	key := sortKey(sortBy)
	sign := 1

	if sortOrder == sortOrderDesc {
		sign = -1
	}

	return func(a, b *secretsdriver.SecretInfo) int {
		if c := key(a, b); c != 0 {
			return sign * c
		}

		return cmp.Compare(a.Name, b.Name)
	}
}

// sortKey returns a three-way comparator for a ListSecrets SortBy value.
func sortKey(sortBy string) func(a, b *secretsdriver.SecretInfo) int {
	switch sortBy {
	case sortByName:
		return func(a, b *secretsdriver.SecretInfo) int { return cmp.Compare(a.Name, b.Name) }
	case sortByLastChangedDate:
		return func(a, b *secretsdriver.SecretInfo) int {
			return cmp.Compare(epochSeconds(a.UpdatedAt), epochSeconds(b.UpdatedAt))
		}
	case sortByLastAccessedDate:
		return func(a, b *secretsdriver.SecretInfo) int {
			return cmp.Compare(epochSeconds(a.LastAccessedDate), epochSeconds(b.LastAccessedDate))
		}
	default: // sortByCreatedDate
		return func(a, b *secretsdriver.SecretInfo) int {
			return cmp.Compare(epochSeconds(a.CreatedAt), epochSeconds(b.CreatedAt))
		}
	}
}

// listCursor is the ListSecrets NextToken payload: the sort-relevant fields of
// the last entry returned, so the next page resumes strictly after it. Unlike
// an offset, a cursor doesn't skip or repeat entries when secrets are created
// or deleted between page calls.
type listCursor struct {
	Name         string `json:"n"`
	CreatedAt    string `json:"c,omitempty"`
	UpdatedAt    string `json:"u,omitempty"`
	LastAccessed string `json:"a,omitempty"`
}

func encodeListCursor(info *secretsdriver.SecretInfo) string {
	b, _ := json.Marshal(listCursor{
		Name: info.Name, CreatedAt: info.CreatedAt, UpdatedAt: info.UpdatedAt, LastAccessed: info.LastAccessedDate,
	})

	return base64.RawURLEncoding.EncodeToString(b)
}

// errInvalidNextToken marks a NextToken that doesn't decode to a cursor; the
// handler answers it with ListSecrets' documented InvalidNextTokenException.
var errInvalidNextToken = errors.New("invalid NextToken")

// msgInvalidNextToken is the InvalidNextTokenException description from the
// Secrets Manager common errors.
const msgInvalidNextToken = "The NextToken value is invalid."

func decodeListCursor(token string) (*secretsdriver.SecretInfo, error) {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, errInvalidNextToken
	}

	var c listCursor
	if err := json.Unmarshal(b, &c); err != nil || c.Name == "" {
		return nil, errInvalidNextToken
	}

	return &secretsdriver.SecretInfo{
		Name: c.Name, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, LastAccessedDate: c.LastAccessed,
	}, nil
}

// listSecretsPage orders infos, then returns the page that starts after the
// NextToken cursor (the first page when token is empty) and the token for the
// following page ("" on the last page). maxResults nil returns everything left.
func listSecretsPage(
	infos []secretsdriver.SecretInfo, req *listSecretsRequest,
) ([]secretsdriver.SecretInfo, string, error) {
	order := secretOrder(req.SortBy, req.SortOrder)

	sort.Slice(infos, func(i, j int) bool { return order(&infos[i], &infos[j]) < 0 })

	start := 0

	if req.NextToken != "" {
		after, err := decodeListCursor(req.NextToken)
		if err != nil {
			return nil, "", err
		}

		start = len(infos)

		for i := range infos {
			if order(after, &infos[i]) < 0 {
				start = i
				break
			}
		}
	}

	end := len(infos)
	if req.MaxResults != nil && start+int(*req.MaxResults) < end {
		end = start + int(*req.MaxResults)
	}

	page := infos[start:end]
	if end == len(infos) || len(page) == 0 {
		return page, "", nil
	}

	return page, encodeListCursor(&page[len(page)-1]), nil
}
