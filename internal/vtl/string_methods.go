package vtl

import (
	"regexp"
	"regexp/syntax"
	"strings"
	"unicode/utf8"
)

// maxPatternBytes caps a regular expression a template passes to a string
// method; compiling a huge pattern costs memory before any match runs.
const maxPatternBytes = 64 << 10

// firstMatchBatch is the first batch eachMatchBatched collects; later
// batches double, each checked against the budget before it runs.
const firstMatchBatch = 64

// stringFn is one String method. It charges to mem whatever it allocates in
// proportion to its input before allocating it.
type stringFn func(mem *budget, s string, args []any) (any, error)

// stringMethods is the Java-like String method bridge.
//
//nolint:gochecknoglobals // read-only dispatch table
var stringMethods = map[string]stringFn{
	"length":           func(_ *budget, s string, _ []any) (any, error) { return int64(utf8.RuneCountInString(s)), nil },
	mIsEmpty:           func(_ *budget, s string, _ []any) (any, error) { return s == "", nil },
	mContains:          strPredicate(strings.Contains),
	"startsWith":       strPredicate(strings.HasPrefix),
	"endsWith":         strPredicate(strings.HasSuffix),
	"equalsIgnoreCase": strPredicate(strings.EqualFold),
	mIndexOf:           strIndex(strings.Index),
	"lastIndexOf":      strIndex(strings.LastIndex),
	"substring":        func(_ *budget, s string, args []any) (any, error) { return substring(s, args), nil },
	"replace":          strReplace,
	"replaceAll":       func(mem *budget, s string, args []any) (any, error) { return regexReplace(mem, s, true, args) },
	"replaceFirst":     func(mem *budget, s string, args []any) (any, error) { return regexReplace(mem, s, false, args) },
	"split":            strSplit,
	"toLowerCase":      func(_ *budget, s string, _ []any) (any, error) { return strings.ToLower(s), nil },
	"toUpperCase":      func(_ *budget, s string, _ []any) (any, error) { return strings.ToUpper(s), nil },
	"trim":             func(_ *budget, s string, _ []any) (any, error) { return strings.TrimSpace(s), nil },
	"matches":          strMatches,
	"charAt":           strCharAt,
}

func strPredicate(pred func(s, arg string) bool) stringFn {
	return func(_ *budget, s string, args []any) (any, error) {
		a, ok := strArg(args, 0)

		return ok && pred(s, a), nil
	}
}

// strIndex converts a byte offset to a character offset (-1 stays -1).
func strIndex(find func(s, sub string) int) stringFn {
	return func(_ *budget, s string, args []any) (any, error) {
		a, _ := strArg(args, 0)

		i := find(s, a)
		if i < 0 {
			return int64(-1), nil
		}

		return int64(utf8.RuneCountInString(s[:i])), nil
	}
}

func strReplace(_ *budget, s string, args []any) (any, error) {
	from, ok1 := strArg(args, 0)
	to, ok2 := strArg(args, 1)

	if !ok1 || !ok2 {
		return nil, nil
	}

	return literalReplace(s, from, to, true)
}

// literalReplace replaces from with to (every occurrence, or the first),
// checking the result size before building it.
func literalReplace(s, from, to string, all bool) (any, error) {
	n := 1
	if all {
		n = strings.Count(s, from)
	}

	if strings.Contains(s, from) && len(s)+n*(len(to)-len(from)) > MaxOutputBytes {
		return nil, ErrOutputLimit
	}

	if !all {
		return strings.Replace(s, from, to, 1), nil
	}

	return strings.ReplaceAll(s, from, to), nil
}

func strMatches(_ *budget, s string, args []any) (any, error) {
	pattern, _ := strArg(args, 0)

	re, err := compile("^(?:" + pattern + ")$")
	if err != nil {
		return nil, err
	}

	return re.MatchString(s), nil
}

// runeAt returns the byte offset of the i-th character of s, or -1 when s has
// fewer than i characters. i == character count yields len(s).
func runeAt(s string, i int) int {
	if i < 0 {
		return -1
	}

	for off := range s {
		if i == 0 {
			return off
		}

		i--
	}

	if i == 0 {
		return len(s)
	}

	return -1
}

func strCharAt(_ *budget, s string, args []any) (any, error) {
	i, ok := intArg(args, 0)
	if !ok {
		return nil, nil
	}

	off := runeAt(s, i)
	if off < 0 || off == len(s) {
		return nil, nil
	}

	_, size := utf8.DecodeRuneInString(s[off:])

	return s[off : off+size], nil
}

func substring(s string, args []any) any {
	begin, ok := intArg(args, 0)
	if !ok {
		return nil
	}

	from := runeAt(s, begin)
	if from < 0 {
		return nil
	}

	end, hasEnd := intArg(args, 1)
	if !hasEnd {
		return s[from:]
	}

	if end < begin {
		return nil
	}

	to := runeAt(s, end)
	if to < 0 {
		return nil
	}

	return s[from:to]
}

func compile(pattern string) (*regexp.Regexp, error) {
	if len(pattern) > maxPatternBytes {
		return nil, errorf("regular expression is longer than %d bytes", maxPatternBytes)
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, errorf("invalid regular expression %q: %v", pattern, err)
	}

	return re, nil
}

// matchCost is what one regex match is charged: its index slice plus the
// regexp engine's per-call state.
const matchCost = 4 * slotBytes

// eachMatch calls fn for each match of re in s, in order, stopping after n
// matches when n >= 0. It finds one match at a time and charges each before
// the next search, so millions of matches fail at the budget instead of being
// collected up front. It follows FindAll's rules: matches do not overlap, and
// an empty match right after the previous match is skipped.
func eachMatch(mem *budget, re *regexp.Regexp, s string, n int, fn func(loc []int) error) error {
	if needsContext(re) {
		return eachMatchBatched(mem, re, s, n, fn)
	}

	return eachMatchIncremental(mem, re, s, n, fn)
}

// eachMatchIncremental searches successive suffixes of s, one match at a time.
func eachMatchIncremental(mem *budget, re *regexp.Regexp, s string, n int, fn func(loc []int) error) error {
	pos, prevEnd := 0, -1

	for count := 0; pos <= len(s) && (n < 0 || count < n); {
		if err := mem.charge(matchCost); err != nil {
			return err
		}

		loc := findFrom(re, s, pos)
		if loc == nil {
			return nil
		}

		// FindAll skips an empty match right after the previous match.
		if loc[0] != loc[1] || loc[0] != prevEnd {
			if err := fn(loc); err != nil {
				return err
			}

			count++
			prevEnd = loc[1]
		}

		var done bool
		if pos, done = nextSearch(s, loc); done {
			return nil
		}
	}

	return nil
}

// findFrom finds the first match of re in s at or after pos, with indexes
// into s.
func findFrom(re *regexp.Regexp, s string, pos int) []int {
	loc := re.FindStringSubmatchIndex(s[pos:])

	for k := range loc {
		if loc[k] >= 0 {
			loc[k] += pos
		}
	}

	return loc
}

// nextSearch returns where to search after match loc: its end, or one
// character further for an empty match. done is true at the end of s.
func nextSearch(s string, loc []int) (pos int, done bool) {
	if loc[0] != loc[1] {
		return loc[1], false
	}

	if loc[1] >= len(s) {
		return 0, true
	}

	_, w := utf8.DecodeRuneInString(s[loc[1]:])

	return loc[1] + w, false
}

// needsContext reports whether re uses an assertion (^, \A, \b, \B) whose
// result depends on the text before the search start, which rules out
// searching a suffix of the string.
func needsContext(re *regexp.Regexp) bool {
	parsed, err := syntax.Parse(re.String(), syntax.Perl)
	if err != nil {
		return true
	}

	return hasContextOp(parsed)
}

func hasContextOp(r *syntax.Regexp) bool {
	if r.Op == syntax.OpBeginLine || r.Op == syntax.OpBeginText ||
		r.Op == syntax.OpWordBoundary || r.Op == syntax.OpNoWordBoundary {
		return true
	}

	for _, sub := range r.Sub {
		if hasContextOp(sub) {
			return true
		}
	}

	return false
}

// eachMatchBatched serves patterns with context assertions: it collects
// matches with FindAll in doubling batches, checking each batch against the
// budget before running it.
func eachMatchBatched(mem *budget, re *regexp.Regexp, s string, n int, fn func(loc []int) error) error {
	for batch := firstMatchBatch; ; batch *= 2 {
		if n >= 0 && batch >= n {
			batch = n
		}

		if err := mem.check(2 * batch * matchCost); err != nil {
			return err
		}

		locs := re.FindAllStringSubmatchIndex(s, batch)
		if len(locs) == batch && batch != n {
			continue
		}

		if err := mem.charge(len(locs) * matchCost); err != nil {
			return err
		}

		for _, loc := range locs {
			if err := fn(loc); err != nil {
				return err
			}
		}

		return nil
	}
}

func regexReplace(mem *budget, s string, all bool, args []any) (any, error) {
	pattern, _ := strArg(args, 0)
	repl, _ := strArg(args, 1)

	// A literal pattern and replacement need no regex engine.
	if regexp.QuoteMeta(pattern) == pattern && !strings.ContainsAny(repl, `$\`) && pattern != "" {
		return literalReplace(s, pattern, repl, all)
	}

	re, err := compile(pattern)
	if err != nil {
		return nil, err
	}

	n := 1
	if all {
		n = -1
	}

	// Build the result match by match so it can stop at the size limit.
	var (
		out  []byte
		last int
	)

	err = eachMatch(mem, re, s, n, func(loc []int) error {
		out = append(out, s[last:loc[0]]...)
		out = re.ExpandString(out, repl, s, loc)
		last = loc[1]

		if len(out) > MaxOutputBytes {
			return ErrOutputLimit
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	out = append(out, s[last:]...)
	if len(out) > MaxOutputBytes {
		return nil, ErrOutputLimit
	}

	return string(out), nil
}

// strSplit follows Java's String.split: the argument is a regex and trailing
// empty strings are dropped. Each piece is charged as it is added, so a huge
// split stops at the budget.
func strSplit(mem *budget, s string, args []any) (any, error) {
	pattern, _ := strArg(args, 0)

	var (
		l   *List
		err error
	)

	if regexp.QuoteMeta(pattern) == pattern && pattern != "" {
		l, err = splitLiteral(mem, s, pattern)
	} else {
		l, err = splitRegex(mem, s, pattern)
	}

	if err != nil {
		return nil, err
	}

	for len(l.Items) > 0 && l.Items[len(l.Items)-1] == "" {
		l.Items = l.Items[:len(l.Items)-1]
	}

	return l, nil
}

func splitLiteral(mem *budget, s, sep string) (*List, error) {
	l := NewList()

	for {
		if err := mem.charge(slotBytes); err != nil {
			return nil, err
		}

		i := strings.Index(s, sep)
		if i < 0 {
			l.Items = append(l.Items, s)

			return l, nil
		}

		l.Items = append(l.Items, s[:i])
		s = s[i+len(sep):]
	}
}

func splitRegex(mem *budget, s, pattern string) (*List, error) {
	re, err := compile(pattern)
	if err != nil {
		return nil, err
	}

	l := NewList()
	last := 0

	err = eachMatch(mem, re, s, -1, func(loc []int) error {
		// Java drops the empty leading piece a zero-width match at 0 makes.
		if loc[1] == 0 {
			return nil
		}

		if cerr := mem.charge(slotBytes); cerr != nil {
			return cerr
		}

		l.Items = append(l.Items, s[last:loc[0]])
		last = loc[1]

		return nil
	})
	if err != nil {
		return nil, err
	}

	l.Items = append(l.Items, s[last:])

	return l, mem.charge(slotBytes)
}
