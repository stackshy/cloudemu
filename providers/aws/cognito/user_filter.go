package cognito

import (
	"regexp"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/cognito/driver"
)

// filterPrefix is the ListUsers "starts with" operator; "=" is an exact match.
const filterPrefix = "^="

// Pseudo-attributes a ListUsers filter can search besides real attributes.
const (
	searchUsername   = "username"
	searchUserStatus = "cognito:user_status"
	searchStatus     = "status"
)

// Values of the "status" search attribute.
const (
	statusEnabled  = "Enabled"
	statusDisabled = "Disabled"
)

// userFilterPattern matches `attr = "value"` or `attr ^= "value"`, with \" and
// \\ escapes inside the quoted value.
var userFilterPattern = regexp.MustCompile(`^\s*([^\s=^]+)\s*(\^=|=)\s*"((?:[^"\\]|\\.)*)"\s*$`)

// searchableAttributes lists what ListUsers can filter on. Custom attributes
// are not searchable.
//
//nolint:gochecknoglobals // read-only lookup table
var searchableAttributes = map[string]bool{
	searchUsername: true, attrEmail: true, attrPhoneNumber: true, "name": true,
	"given_name": true, "family_name": true, attrPreferredUsername: true,
	searchUserStatus: true, searchStatus: true, attrSub: true,
}

// userFilter is a parsed ListUsers filter. The zero value matches every user.
type userFilter struct {
	attr  string
	op    string
	value string
}

// parseUserFilter parses a ListUsers Filter. An empty filter matches all users.
func parseUserFilter(s string) (userFilter, error) {
	if strings.TrimSpace(s) == "" {
		return userFilter{}, nil
	}

	m := userFilterPattern.FindStringSubmatch(s)
	if m == nil {
		return userFilter{}, invalidParameter("Error while parsing filter.")
	}

	if !searchableAttributes[m[1]] {
		return userFilter{}, invalidParameter("Invalid search attribute: %s", m[1])
	}

	value := strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(m[3])

	return userFilter{attr: m[1], op: m[2], value: value}, nil
}

// matches reports whether a user passes the filter. cognito:user_status
// compares case-insensitively; everything else is case-sensitive.
func (f userFilter) matches(u *driver.User) bool {
	if f.attr == "" {
		return true
	}

	got, ok := f.lookup(u)
	if !ok {
		return false
	}

	want := f.value
	if f.attr == searchUserStatus {
		got, want = strings.ToUpper(got), strings.ToUpper(want)
	}

	if f.op == filterPrefix {
		return strings.HasPrefix(got, want)
	}

	return got == want
}

func (f userFilter) lookup(u *driver.User) (string, bool) {
	switch f.attr {
	case searchUsername:
		return u.Username, true
	case searchUserStatus:
		return u.UserStatus, true
	case searchStatus:
		if u.Enabled {
			return statusEnabled, true
		}

		return statusDisabled, true
	default:
		return lastValue(u.Attributes, f.attr)
	}
}
