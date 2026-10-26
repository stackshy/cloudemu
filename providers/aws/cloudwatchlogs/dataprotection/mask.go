package dataprotection

import (
	"net"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// The managed identifiers this emulator detects. Every regex runs on Go's RE2
// engine, which matches in time linear in the input.
const (
	managedEmail      = "EmailAddress"
	managedIP         = "IpAddress"
	managedSecretKey  = "AwsSecretKey"
	managedCreditCard = "CreditCardNumber"
)

var (
	emailRE = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`)
	ipv4RE  = regexp.MustCompile(
		`\b(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\b`)
	ipv6RE = regexp.MustCompile(`[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{0,4}){2,7}`)
	// AwsSecretKey needs a keyword next to the 40-character key, as on AWS.
	secretKeyRE = regexp.MustCompile(
		`(?i)(?:aws_?)?secret_?(?:access_?)?key["']?\s*[:=]\s*["']?([A-Za-z0-9/+]{40})(?:[^A-Za-z0-9/+=]|$)`)
	creditCardRE = regexp.MustCompile(`\b\d(?:[ -]?\d){12,18}\b`)
)

// detector finds one kind of sensitive data. group selects the submatch to
// mask (0 is the whole match); valid, when set, confirms a candidate.
type detector struct {
	re    *regexp.Regexp
	group int
	valid func(string) bool
}

func isManagedName(name string) bool {
	_, ok := managedDetector(name)
	return ok
}

func managedDetector(name string) (detector, bool) {
	switch name {
	case managedEmail:
		return detector{re: emailRE}, true
	case managedSecretKey:
		return detector{re: secretKeyRE, group: 1}, true
	case managedCreditCard:
		return detector{re: creditCardRE, valid: luhn}, true
	case managedIP:
		return detector{re: ipv4RE}, true
	default:
		return detector{}, false
	}
}

// ipv6Detector backs IpAddress alongside ipv4RE: the candidate must parse as
// an IPv6 address.
func ipv6Detector() detector {
	return detector{re: ipv6RE, valid: func(s string) bool {
		ip := net.ParseIP(s)
		return ip != nil && ip.To4() == nil && strings.ContainsAny(s, "0123456789abcdefABCDEF")
	}}
}

func (p *Policy) appendIPv6() {
	for _, id := range p.identifiers {
		if id == managedARNPrefix+managedIP {
			p.detectors = append(p.detectors, ipv6Detector())
			return
		}
	}
}

// Luhn check constants: the base, and the most a doubled digit may be before
// its digits are summed.
const (
	luhnBase        = 10
	luhnMaxDoubled  = 9
	minCardDigits   = 13
	luhnDoubleEvery = 2
)

func luhn(s string) bool {
	sum, n := 0, 0

	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if c < '0' || c > '9' {
			continue
		}

		d := int(c - '0')
		if n%luhnDoubleEvery == 1 {
			d *= luhnDoubleEvery
			if d > luhnMaxDoubled {
				d -= luhnMaxDoubled
			}
		}

		sum += d
		n++
	}

	return n >= minCardDigits && sum%luhnBase == 0
}

type span struct{ start, end int }

// Mask replaces every match of the policies' identifiers in msg with one
// asterisk per character. It reports whether anything was masked.
func Mask(msg string, policies ...*Policy) (string, bool) {
	var spans []span

	for _, p := range policies {
		if p == nil {
			continue
		}

		for _, d := range p.detectors {
			spans = d.find(msg, spans)
		}
	}

	if len(spans) == 0 {
		return msg, false
	}

	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })

	var b strings.Builder

	last := 0

	for _, s := range spans {
		if s.end <= last {
			continue
		}

		start := max(s.start, last)
		b.WriteString(msg[last:start])
		b.WriteString(strings.Repeat("*", utf8.RuneCountInString(msg[start:s.end])))
		last = s.end
	}

	b.WriteString(msg[last:])

	return b.String(), true
}

func (d *detector) find(msg string, spans []span) []span {
	for _, m := range d.re.FindAllStringSubmatchIndex(msg, -1) {
		start, end := m[2*d.group], m[2*d.group+1]
		if start < 0 || end <= start {
			continue
		}

		if d.valid != nil && !d.valid(msg[start:end]) {
			continue
		}

		spans = append(spans, span{start, end})
	}

	return spans
}
