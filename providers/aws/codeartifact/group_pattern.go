package codeartifact

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

const (
	minPatternLen = 2
	maxPatternLen = 520
)

type nsKind int

const (
	nsAll nsKind = iota
	nsBlank
	nsExact
	nsPrefix
)

type nameKind int

const (
	nameAll nameKind = iota
	nameExact
	namePrefix
)

// groupPattern is a parsed package group pattern. A package path is
// /<format>/<namespace>/<name>; a pattern is a path with trailing components
// omitted and a suffix: $ matches the full coordinate, ~ a word-boundary prefix
// and * every value of the component before it.
type groupPattern struct {
	allFormats bool
	format     string
	nsKind     nsKind
	ns         string
	nameKind   nameKind
	name       string
}

// isWordChar reports whether r continues a word: a letter, number or mark.
func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r)
}

// prefixWord reports whether s starts with p ending on a word boundary: s is p,
// or the character after p is not a word character.
func prefixWord(s, p string) bool {
	if !strings.HasPrefix(s, p) {
		return false
	}

	if len(s) == len(p) {
		return true
	}

	r, _ := utf8.DecodeRuneInString(s[len(p):])

	return !isWordChar(r)
}

// normalizeVariant folds the differences weak matching ignores: letter case and
// runs of dash, dot and underscore (each run becomes one dot).
func normalizeVariant(s string) string {
	var b strings.Builder

	lastSep := false

	for _, r := range strings.ToLower(s) {
		if r == '-' || r == '.' || r == '_' {
			if !lastSep {
				b.WriteByte('.')
			}

			lastSep = true

			continue
		}

		lastSep = false

		b.WriteRune(r)
	}

	return b.String()
}

// validLiteral reports whether a namespace or name literal of a pattern is
// non-empty and free of the pattern's special characters.
func validLiteral(s string) bool {
	return s != "" && !strings.ContainsAny(s, "/$~*") && !hasSpace(s) && !hasControl(s)
}

// endsInWord reports whether the character before a ~ suffix is a word character.
func endsInWord(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)

	return isWordChar(r)
}

// validPatternText reports whether p has an allowed length and no whitespace or
// control characters.
func validPatternText(p string) bool {
	n := utf8.RuneCountInString(p)

	return n >= minPatternLen && n <= maxPatternLen && !hasSpace(p) && !hasControl(p)
}

// parseGroupPattern parses and validates a package group pattern.
func parseGroupPattern(p string) (groupPattern, error) {
	if !validPatternText(p) {
		return groupPattern{}, validation("package group pattern must be 2-520 characters without whitespace")
	}

	rest, ok := strings.CutPrefix(p, "/")
	if !ok {
		return groupPattern{}, badPattern(p)
	}

	if rest == "*" {
		return groupPattern{allFormats: true, nsKind: nsAll, nameKind: nameAll}, nil
	}

	format, tail, found := strings.Cut(rest, "/")
	if !found || !validFormat(format) {
		return groupPattern{}, badPattern(p)
	}

	gp := groupPattern{format: format}
	if !gp.parseTail(tail) || !gp.formatAllowsShape() {
		return groupPattern{}, badPattern(p)
	}

	return gp, nil
}

func badPattern(p string) error {
	return validation("package group pattern %q is not valid: use /*, /<format>/*, /<format>/<namespace>~, "+
		"/<format>/<namespace>/*, /<format>/<namespace>/<name>~ or /<format>/<namespace>/<name>$", p)
}

// parseTail parses the part of a pattern after /<format>/ and reports whether it
// is well formed.
func (gp *groupPattern) parseTail(tail string) bool {
	if tail == "*" {
		gp.nsKind, gp.nameKind = nsAll, nameAll

		return true
	}

	slash := strings.IndexByte(tail, '/')
	if slash < 0 {
		ns, ok := strings.CutSuffix(tail, "~")
		if !ok || !validLiteral(ns) || !endsInWord(ns) {
			return false
		}

		gp.nsKind, gp.ns, gp.nameKind = nsPrefix, ns, nameAll

		return true
	}

	gp.nsKind = nsBlank

	if ns := tail[:slash]; ns != "" {
		if !validLiteral(ns) {
			return false
		}

		gp.nsKind, gp.ns = nsExact, ns
	}

	return gp.parseName(tail[slash+1:])
}

// parseName parses the name part of a pattern.
func (gp *groupPattern) parseName(name string) bool {
	if name == "*" {
		gp.nameKind = nameAll

		return true
	}

	if lit, ok := strings.CutSuffix(name, "~"); ok {
		if !validLiteral(lit) || !endsInWord(lit) {
			return false
		}

		gp.nameKind, gp.name = namePrefix, lit

		return true
	}

	if lit, ok := strings.CutSuffix(name, "$"); ok {
		if !validLiteral(lit) {
			return false
		}

		gp.nameKind, gp.name = nameExact, lit

		return true
	}

	return false
}

// formatAllowsShape applies the per-format namespace rules: Python and NuGet
// have no namespace, and Maven, generic and Swift packages always have one.
func (gp *groupPattern) formatAllowsShape() bool {
	switch gp.format {
	case driver.FormatPyPI, driver.FormatNuGet:
		return gp.nsKind == nsAll || gp.nsKind == nsBlank
	case driver.FormatMaven, driver.FormatGeneric, driver.FormatSwift:
		return gp.nsKind != nsBlank
	default:
		return true
	}
}

// covers reports whether every package matching a also matches gp, comparing the
// patterns structurally.
func (gp groupPattern) covers(a groupPattern) bool {
	if !gp.allFormats && (a.allFormats || a.format != gp.format) {
		return false
	}

	return gp.coversNamespace(a) && gp.coversName(a)
}

func (gp groupPattern) coversNamespace(a groupPattern) bool {
	switch gp.nsKind {
	case nsAll:
		return true
	case nsBlank:
		return a.nsKind == nsBlank
	case nsExact:
		return a.nsKind == nsExact && a.ns == gp.ns
	case nsPrefix:
		return (a.nsKind == nsExact || a.nsKind == nsPrefix) && prefixWord(a.ns, gp.ns)
	default:
		return false
	}
}

func (gp groupPattern) coversName(a groupPattern) bool {
	switch gp.nameKind {
	case nameAll:
		return true
	case nameExact:
		return a.nameKind == nameExact && a.name == gp.name
	case namePrefix:
		return a.nameKind != nameAll && prefixWord(a.name, gp.name)
	default:
		return false
	}
}

// matches reports whether a package matches the pattern. A strong match is exact;
// a weak match ignores case and runs of dash, dot and underscore.
func (gp groupPattern) matches(format, ns, name string, weak bool) bool {
	if !gp.allFormats && gp.format != format {
		return false
	}

	fold := func(s string) string { return s }
	if weak {
		fold = normalizeVariant
	}

	return gp.matchesNamespace(fold(ns), fold) && gp.matchesName(fold(name), fold)
}

func (gp groupPattern) matchesNamespace(ns string, fold func(string) string) bool {
	switch gp.nsKind {
	case nsAll:
		return true
	case nsBlank:
		return ns == ""
	case nsExact:
		return ns == fold(gp.ns)
	case nsPrefix:
		return prefixWord(ns, fold(gp.ns))
	default:
		return false
	}
}

func (gp groupPattern) matchesName(name string, fold func(string) string) bool {
	switch gp.nameKind {
	case nameAll:
		return true
	case nameExact:
		return name == fold(gp.name)
	case namePrefix:
		return prefixWord(name, fold(gp.name))
	default:
		return false
	}
}
