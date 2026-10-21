package filterpattern

import (
	"regexp"
	"strings"
	"unicode"
)

// regexAllowedSymbols are the non-alphanumeric characters a %regex% may use:
// the documented symbol set, the operators, and the escape character. A
// literal space is not in the set; match one with \s or \x20.
const regexAllowedSymbols = ":_#=@/;,-^$?[]{}|\\*+."

// compileRegex validates a %...% body against the CloudWatch Logs regex
// subset (no parentheses, no multi-byte characters, a fixed symbol set) and
// compiles it. The match is unanchored unless the body uses ^ or $.
func compileRegex(body string) (*regexp.Regexp, error) {
	if body == "" {
		return nil, invalid("Invalid regex in filter pattern: empty expression")
	}

	if err := checkRegexChars(body); err != nil {
		return nil, err
	}

	re, err := regexp.Compile(body)
	if err != nil {
		return nil, invalid("Invalid regex in filter pattern: %" + body + "%")
	}

	return re, nil
}

func checkRegexChars(body string) error {
	for _, r := range body {
		if r > unicode.MaxASCII {
			return invalid("Invalid regex in filter pattern: multi-byte characters are not supported")
		}

		if !isASCIIAlnum(r) && !strings.ContainsRune(regexAllowedSymbols, r) {
			return invalid("Invalid character(s) in regex %" + body + "%")
		}
	}

	return nil
}

func isASCIIAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}
