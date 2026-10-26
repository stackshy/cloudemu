package dataprotection

import (
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/errors"
)

const arn = "arn:aws:dataprotection::aws:data-identifier/"

// doc builds a two-statement policy over ids, with an optional Configuration.
func doc(ids, config string) string {
	cfg := ""
	if config != "" {
		cfg = `"Configuration":` + config + `,`
	}

	return `{"Name":"p","Version":"2021-06-01",` + cfg + `"Statement":[
{"Sid":"audit","DataIdentifier":[` + ids + `],"Operation":{"Audit":{"FindingsDestination":{}}}},
{"Sid":"redact","DataIdentifier":[` + ids + `],"Operation":{"Deidentify":{"MaskConfig":{}}}}]}`
}

func TestParseRejectsInvalidPolicies(t *testing.T) {
	email := `"` + arn + `EmailAddress"`
	ip := `"` + arn + `IpAddress"`

	tests := []struct {
		name string
		doc  string
	}{
		{"not JSON", `{"Statement":`},
		{"trailing data", doc(email, "") + `{}`},
		{"wrong version", strings.Replace(doc(email, ""), "2021-06-01", "2012-10-17", 1)},
		{"one statement", `{"Statement":[{"DataIdentifier":[` + email + `],"Operation":{"Audit":{"FindingsDestination":{}}}}]}`},
		{"two audits", strings.Replace(doc(email, ""), `{"Deidentify":{"MaskConfig":{}}}`, `{"Audit":{"FindingsDestination":{}}}`, 1)},
		{"audit without destination", strings.Replace(doc(email, ""), `"FindingsDestination":{}`, `"X":{}`, 1)},
		{"non-empty MaskConfig", strings.Replace(doc(email, ""), `"MaskConfig":{}`, `"MaskConfig":{"a":1}`, 1)},
		{"missing MaskConfig", strings.Replace(doc(email, ""), `"MaskConfig":{}`, `"Other":{}`, 1)},
		{"unknown operation", strings.Replace(doc(email, ""), `"Deidentify"`, `"Redact"`, 1)},
		{"two operations", strings.Replace(doc(email, ""), `{"Deidentify":{"MaskConfig":{}}}`,
			`{"Deidentify":{"MaskConfig":{}},"Audit":{"FindingsDestination":{}}}`, 1)},
		{"mismatched identifiers", strings.Replace(doc(email, ""), `"Sid":"redact","DataIdentifier":[`+email,
			`"Sid":"redact","DataIdentifier":[`+ip, 1)},
		{"empty identifiers", doc("", "")},
		{"undefined custom identifier", doc(`"EmployeeId"`, "")},
		{"bad managed ARN", doc(`"`+arn+`Email Address"`, "")},
		{"custom named like a managed identifier", doc(`"EmailAddress"`,
			`{"CustomDataIdentifier":[{"Name":"EmailAddress","Regex":"x"}]}`)},
		{"custom regex too long", doc(`"Emp"`,
			`{"CustomDataIdentifier":[{"Name":"Emp","Regex":"`+strings.Repeat("a", 201)+`"}]}`)},
		{"custom regex bad character", doc(`"Emp"`, `{"CustomDataIdentifier":[{"Name":"Emp","Regex":"a&b"}]}`)},
		{"custom regex does not compile", doc(`"Emp"`, `{"CustomDataIdentifier":[{"Name":"Emp","Regex":"a{2000}"}]}`)},
		{"custom name invalid", doc(`"E.mp"`, `{"CustomDataIdentifier":[{"Name":"E.mp","Regex":"x"}]}`)},
		{"duplicate custom", doc(`"Emp"`,
			`{"CustomDataIdentifier":[{"Name":"Emp","Regex":"x"},{"Name":"Emp","Regex":"y"}]}`)},
		{"too long", doc(email, "") + strings.Repeat(" ", MaxDocumentLength)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse(tt.doc); !errors.IsInvalidArgument(err) {
				t.Fatalf("Parse = %v, want InvalidArgument", err)
			}
		})
	}
}

func TestParseCustomIdentifierLimit(t *testing.T) {
	var defs, ids []string

	for i := range 11 {
		n := "C" + strings.Repeat("x", i)
		defs = append(defs, `{"Name":"`+n+`","Regex":"x"}`)
		ids = append(ids, `"`+n+`"`)
	}

	cfg := `{"CustomDataIdentifier":[` + strings.Join(defs, ",") + `]}`
	if _, err := Parse(doc(strings.Join(ids, ","), cfg)); !errors.IsInvalidArgument(err) {
		t.Fatalf("11 custom identifiers = %v, want InvalidArgument", err)
	}

	cfg = `{"CustomDataIdentifier":[` + strings.Join(defs[:10], ",") + `]}`
	if _, err := Parse(doc(strings.Join(ids[:10], ","), cfg)); err != nil {
		t.Fatalf("10 custom identifiers: %v", err)
	}
}

func TestParseAcceptsAWSExample(t *testing.T) {
	p, err := Parse(`{"Name":"data-protection-policy","Description":"test description","Version":"2021-06-01",
"Statement":[{"Sid":"audit-policy","DataIdentifier":["` + arn + `EmailAddress","` + arn + `DriversLicense-US"],
"Operation":{"Audit":{"FindingsDestination":{"CloudWatchLogs":{"LogGroup":"findings"},"S3":{"Bucket":"b"}}}}},
{"Sid":"redact-policy","DataIdentifier":["` + arn + `EmailAddress","` + arn + `DriversLicense-US"],
"Operation":{"Deidentify":{"MaskConfig":{ }}}}]}`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if p.FindingsLogGroup() != "findings" {
		t.Fatalf("FindingsLogGroup = %q", p.FindingsLogGroup())
	}

	if got := p.Identifiers(); len(got) != 2 {
		t.Fatalf("Identifiers = %v", got)
	}

	// DriversLicense-US is accepted but not detected; the email still is.
	if got, _ := Mask("mail a@b.io lic D1234567", p); got != "mail ****** lic D1234567" {
		t.Fatalf("Mask = %q", got)
	}
}

func TestMask(t *testing.T) {
	all := doc(`"`+arn+`EmailAddress","`+arn+`IpAddress","`+arn+`AwsSecretKey","`+arn+`CreditCardNumber","Emp"`,
		`{"CustomDataIdentifier":[{"Name":"Emp","Regex":"EmployeeID-\\d{9}"}]}`)

	p, err := Parse(all)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	tests := []struct {
		name, in, want string
		masked         bool
	}{
		{"email", "user alice@example.com logged in", "user ***************** logged in", true},
		{"ipv4", "from 10.0.0.12 ok", "from ********* ok", true},
		{"not an ipv4", "version 1.2.3 and 999.1.1.1", "version 1.2.3 and 999.1.1.1", false},
		{"ipv6", "from 2001:db8::1 ok", "from *********** ok", true},
		{"clock time is not ipv6", "at 12:30:45 ok", "at 12:30:45 ok", false},
		{"secret key", "aws_secret_access_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY end",
			"aws_secret_access_key=**************************************** end", true},
		{"40 chars without keyword", "token wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "token wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", false},
		{"credit card", "card 4111 1111 1111 1111 ok", "card ******************* ok", true},
		{"luhn failure", "card 4111 1111 1111 1112 ok", "card 4111 1111 1111 1112 ok", false},
		{"custom", "id EmployeeID-123456789.", "id ********************.", true},
		{"nothing", "plain text", "plain text", false},
		{"non-ASCII prefix kept", "é a@x.io", "é ******", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, masked := Mask(tt.in, p)
			if got != tt.want || masked != tt.masked {
				t.Fatalf("Mask(%q) = %q, %v; want %q, %v", tt.in, got, masked, tt.want, tt.masked)
			}
		})
	}
}

func TestMaskCumulativeAndNil(t *testing.T) {
	email, err := Parse(doc(`"`+arn+`EmailAddress"`, ""))
	if err != nil {
		t.Fatal(err)
	}

	ip, err := Parse(doc(`"`+arn+`IpAddress"`, ""))
	if err != nil {
		t.Fatal(err)
	}

	if got, _ := Mask("a@b.io 1.2.3.4", email, nil, ip); got != "****** *******" {
		t.Fatalf("cumulative Mask = %q", got)
	}

	if got, masked := Mask("a@b.io"); got != "a@b.io" || masked {
		t.Fatalf("no policies = %q, %v", got, masked)
	}
}

func TestMaskLinearOnHostileInput(t *testing.T) {
	p, err := Parse(doc(`"Evil"`, `{"CustomDataIdentifier":[{"Name":"Evil","Regex":"(a+)+b"}]}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// A backtracking engine takes exponential time to fail this match; RE2
	// fails it in linear time.
	in := strings.Repeat("a", 100000) + "!"
	if _, masked := Mask(in, p); masked {
		t.Fatal("(a+)+b must not match a run of a's")
	}
}
