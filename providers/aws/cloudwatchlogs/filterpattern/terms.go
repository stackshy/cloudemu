package filterpattern

import (
	"regexp"
	"strings"
)

// termKind is how a term in an unstructured pattern combines with the rest.
type termKind int

const (
	termInclude  termKind = iota // plain or quoted term: must be present
	termExclude                  // -term: must be absent
	termOptional                 // ?term: any one of the optional terms
)

// unquotedForbidden are characters an unquoted term cannot contain; terms
// with such characters must be double-quoted.
const unquotedForbidden = `{}[]()"%<>!&|`

// delimiterPair is the opening and closing quote (or %) around a term.
const delimiterPair = 2

type term struct {
	kind termKind
	text string
	re   *regexp.Regexp
	// any is set for a quoted term that is only whitespace (`" "`), which
	// matches every event.
	any bool
}

func (t *term) present(message string) bool {
	switch {
	case t.any:
		return true
	case t.re != nil:
		return t.re.MatchString(message)
	default:
		return strings.Contains(message, t.text)
	}
}

// termPattern is an unstructured pattern: terms are matched as
// case-sensitive substrings of the message.
type termPattern struct {
	include, exclude, optional []*term
	hasRegex                   bool
}

// match applies the documented rules: every include term present and no
// exclude term present. Optional (?) terms only count when the pattern has no
// other terms; combined with other terms they are ignored.
func (p *termPattern) match(message string) bool {
	if len(p.include) == 0 && len(p.exclude) == 0 {
		for _, t := range p.optional {
			if t.present(message) {
				return true
			}
		}

		return false
	}

	for _, t := range p.include {
		if !t.present(message) {
			return false
		}
	}

	for _, t := range p.exclude {
		if t.present(message) {
			return false
		}
	}

	return true
}

func parseTerms(s string) (*termPattern, error) {
	p := &termPattern{}

	for i := 0; i < len(s); {
		if s[i] == ' ' || s[i] == '\t' {
			i++
			continue
		}

		t, next, err := readTerm(s, i)
		if err != nil {
			return nil, err
		}

		i = next

		if t.re != nil {
			p.hasRegex = true
		}

		switch t.kind {
		case termExclude:
			p.exclude = append(p.exclude, t)
		case termOptional:
			p.optional = append(p.optional, t)
		case termInclude:
			p.include = append(p.include, t)
		}
	}

	return p, nil
}

// readTerm reads one term starting at s[i] and returns it with the index just
// past it.
func readTerm(s string, i int) (*term, int, error) {
	t := &term{kind: termInclude}

	if s[i] == '-' || s[i] == '?' {
		t.kind = termOptional
		if s[i] == '-' {
			t.kind = termExclude
		}

		i++
		if i >= len(s) || s[i] == ' ' {
			return nil, 0, invalid("Invalid filter pattern: '" + s[i-1:i] + "' must prefix a term")
		}
	}

	switch s[i] {
	case '"':
		return readQuotedTerm(t, s, i)
	case '%':
		return readRegexTerm(t, s, i)
	}

	end := strings.IndexAny(s[i:], " \t")
	if end < 0 {
		end = len(s) - i
	}

	t.text = s[i : i+end]
	if strings.ContainsAny(t.text, unquotedForbidden) {
		return nil, 0, invalid("Invalid character(s) in term '" + t.text + "'")
	}

	return t, i + end, nil
}

func readQuotedTerm(t *term, s string, i int) (*term, int, error) {
	end := strings.IndexByte(s[i+1:], '"')
	if end < 0 {
		return nil, 0, invalid("Invalid filter pattern: unterminated quoted term")
	}

	t.text = s[i+1 : i+1+end]
	t.any = strings.TrimSpace(t.text) == ""

	return t, i + end + delimiterPair, nil
}

func readRegexTerm(t *term, s string, i int) (*term, int, error) {
	end := strings.IndexByte(s[i+1:], '%')
	if end < 0 {
		return nil, 0, invalid("Invalid filter pattern: unterminated regex")
	}

	re, err := compileRegex(s[i+1 : i+1+end])
	if err != nil {
		return nil, 0, err
	}

	t.re = re

	return t, i + end + delimiterPair, nil
}
