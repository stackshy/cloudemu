package codeartifact

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// Limits of the CodeArtifact API model and the service quotas.
const (
	minDomainLen     = 2
	maxDomainLen     = 50
	minRepoLen       = 2
	maxRepoLen       = 100
	ownerLen         = 12
	maxDescription   = 1000
	maxComponentLen  = 255
	maxTagKeyLen     = 128
	maxTagValueLen   = 256
	maxTags          = 200
	maxEncryptionKey = 1011
	maxPolicyRevLen  = 100
	maxPolicyBytes   = 7168
	maxBulkVersions  = 100
	sha256HexLen     = 64

	maxDomains          = 10
	maxReposPerDomain   = 1000
	maxUpstreams        = 10
	maxAssetsPerVersion = 350
)

func isLower(c byte) bool { return c >= 'a' && c <= 'z' }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isAlnum(c byte) bool { return isLower(c) || (c >= 'A' && c <= 'Z') || isDigit(c) }

// validDomainName reports whether s matches [a-z][a-z0-9\-]{0,48}[a-z0-9].
func validDomainName(s string) bool {
	if len(s) < minDomainLen || len(s) > maxDomainLen {
		return false
	}

	last := s[len(s)-1]
	if !isLower(s[0]) || !(isLower(last) || isDigit(last)) {
		return false
	}

	for i := 1; i < len(s)-1; i++ {
		if c := s[i]; !isLower(c) && !isDigit(c) && c != '-' {
			return false
		}
	}

	return true
}

// validRepositoryName reports whether s matches [A-Za-z0-9][A-Za-z0-9._\-]{1,99}.
func validRepositoryName(s string) bool {
	if len(s) < minRepoLen || len(s) > maxRepoLen || !isAlnum(s[0]) {
		return false
	}

	for i := 1; i < len(s); i++ {
		if c := s[i]; !isAlnum(c) && c != '.' && c != '_' && c != '-' {
			return false
		}
	}

	return true
}

// validOwner reports whether s is a 12-digit account id.
func validOwner(s string) bool {
	if len(s) != ownerLen {
		return false
	}

	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}

	return true
}

// hasControl reports whether s contains a Unicode "other" (category C) character.
func hasControl(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.C) {
			return true
		}
	}

	return false
}

func hasSpace(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) {
			return true
		}
	}

	return false
}

// validComponent reports whether s is a valid package namespace, name or
// version: 1 to 255 characters, none of # / or whitespace.
func validComponent(s string) bool {
	n := utf8.RuneCountInString(s)

	return n >= 1 && n <= maxComponentLen && !strings.ContainsAny(s, "#/") && !hasSpace(s)
}

// validAssetName reports whether s is a valid asset name: 1 to 255 characters
// without control characters.
func validAssetName(s string) bool {
	n := utf8.RuneCountInString(s)

	return n >= 1 && n <= maxComponentLen && !hasControl(s)
}

func validFormat(s string) bool {
	switch s {
	case driver.FormatNPM, driver.FormatPyPI, driver.FormatMaven, driver.FormatNuGet,
		driver.FormatGeneric, driver.FormatRuby, driver.FormatSwift, driver.FormatCargo:
		return true
	default:
		return false
	}
}

// validateOwner checks an optional domain-owner argument.
func validateOwner(owner string) error {
	if owner != "" && !validOwner(owner) {
		return validation("domain owner %q must be a 12-digit account id", owner)
	}

	return nil
}

func validateDomainName(name string) error {
	if !validDomainName(name) {
		return validation("domain name %q must be 2-50 characters: a lowercase letter, then lowercase letters, digits "+
			"or hyphens, ending in a letter or digit", name)
	}

	return nil
}

func validateRepositoryName(name string) error {
	if !validRepositoryName(name) {
		return validation("repository name %q must be 2-100 characters: a letter or digit, then letters, digits, "+
			"periods, underscores or hyphens", name)
	}

	return nil
}

func validateDescription(desc string) error {
	if utf8.RuneCountInString(desc) > maxDescription || hasControl(desc) {
		return validation("description must be at most %d characters without control characters", maxDescription)
	}

	return nil
}

// validateDomainRef checks the name and owner of a domain argument.
func validateDomainRef(ref driver.DomainRef) error {
	if err := validateDomainName(ref.Domain); err != nil {
		return err
	}

	return validateOwner(ref.Owner)
}

// validateRepositoryRef checks the names and owner of a repository argument.
func validateRepositoryRef(ref driver.RepositoryRef) error {
	if err := validateDomainRef(driver.DomainRef{Domain: ref.Domain, Owner: ref.Owner}); err != nil {
		return err
	}

	return validateRepositoryName(ref.Repository)
}

// validatePackageRef checks the coordinates of a package argument.
func validatePackageRef(ref *driver.PackageRef) error {
	err := validateRepositoryRef(driver.RepositoryRef{Domain: ref.Domain, Owner: ref.Owner, Repository: ref.Repository})
	if err != nil {
		return err
	}

	if !validFormat(ref.Format) {
		return validation("package format %q is not one of npm, pypi, maven, nuget, generic, ruby, swift, cargo", ref.Format)
	}

	if ref.Namespace != "" && !validComponent(ref.Namespace) {
		return validation("package namespace %q is invalid", ref.Namespace)
	}

	if !validComponent(ref.Package) {
		return validation("package name %q is invalid", ref.Package)
	}

	return nil
}

func validateVersion(version string) error {
	if !validComponent(version) {
		return validation("package version %q is invalid", version)
	}

	return nil
}

// validateTags checks one request's tag list against the model limits.
func validateTags(tags map[string]string) error {
	if len(tags) > maxTags {
		return validation("a request may carry at most %d tags", maxTags)
	}

	for k, v := range tags {
		kn := utf8.RuneCountInString(k)
		if kn < 1 || kn > maxTagKeyLen || hasControl(k) {
			return validation("tag key %q must be 1-%d characters without control characters", k, maxTagKeyLen)
		}

		if utf8.RuneCountInString(v) > maxTagValueLen || hasControl(v) {
			return validation("tag value for %q must be at most %d characters without control characters", k, maxTagValueLen)
		}
	}

	return nil
}

// validatePolicyRevision checks an optional policy revision argument.
func validatePolicyRevision(rev string) error {
	if rev != "" && (len(rev) > maxPolicyRevLen || hasSpace(rev)) {
		return validation("policy revision must be 1-%d characters without whitespace", maxPolicyRevLen)
	}

	return nil
}
