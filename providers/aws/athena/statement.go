package athena

import (
	"strings"
	"unicode"

	"github.com/stackshy/cloudemu/v2/services/athena/driver"
)

// kwCreate and kwDrop are SQL keywords shared across the classifier and the DDL
// parser.
const (
	kwCreate = "CREATE"
	kwDrop   = "DROP"
)

// ddlHeadTokens is the length of a database DDL head ("CREATE DATABASE").
const ddlHeadTokens = 2

// classifyStatement derives Athena's StatementType from the query text. DDL
// covers CREATE/ALTER/DROP/MSCK/TRUNCATE (except CTAS, which is DML); UTILITY
// covers SHOW/DESCRIBE/EXPLAIN/USE; everything else (SELECT/INSERT/WITH/CTAS)
// is DML.
func classifyStatement(query string) string {
	tokens := strings.Fields(strings.ToUpper(query))
	if len(tokens) == 0 {
		return driver.StatementTypeDML
	}

	switch tokens[0] {
	case "SELECT", "INSERT", "WITH", "UNLOAD":
		return driver.StatementTypeDML
	case "SHOW", "DESCRIBE", "DESC", "EXPLAIN", "USE":
		return driver.StatementTypeUtility
	case kwCreate, "ALTER", kwDrop, "MSCK", "TRUNCATE":
		if isCreateTableAsSelect(tokens) {
			return driver.StatementTypeDML
		}

		return driver.StatementTypeDDL
	default:
		return driver.StatementTypeDML
	}
}

// isCreateTableAsSelect reports whether the tokens are a CTAS statement, which
// Athena classifies as DML rather than DDL.
func isCreateTableAsSelect(tokens []string) bool {
	if len(tokens) < 2 || tokens[0] != kwCreate || tokens[1] != "TABLE" {
		return false
	}

	for _, t := range tokens {
		if t == "AS" {
			return true
		}
	}

	return false
}

// Database DDL actions.
const (
	actionCreateDatabase = "createDatabase"
	actionDropDatabase   = "dropDatabase"
)

// ddlEffect is the catalog mutation a database DDL statement performs.
type ddlEffect struct {
	action     string // actionCreateDatabase, actionDropDatabase or ""
	database   string
	ifClause   bool // IF NOT EXISTS (create) / IF EXISTS (drop)
	comment    string
	location   string
	properties map[string]string
	cascade    bool
	syntaxErr  string
}

// ddlToken is one lexical token of a DDL statement. quoted marks a string
// literal or a quoted identifier.
type ddlToken struct {
	text   string
	quoted bool
}

// is reports whether t is the unquoted keyword or punctuation kw.
func (t ddlToken) is(kw string) bool { return !t.quoted && strings.EqualFold(t.text, kw) }

// parseDatabaseDDL recognizes CREATE/DROP DATABASE (or SCHEMA) statements:
//
//	CREATE DATABASE [IF NOT EXISTS] name [COMMENT 'c'] [LOCATION 'uri']
//	    [WITH DBPROPERTIES ('k'='v', ...)]
//	DROP DATABASE [IF EXISTS] name [RESTRICT | CASCADE]
//
// Any other statement yields an empty action, which the executor treats as a
// no-op that still settles SUCCEEDED.
func parseDatabaseDDL(query string) ddlEffect {
	toks := lexDDL(query)
	if len(toks) < ddlHeadTokens || (!toks[1].is("DATABASE") && !toks[1].is("SCHEMA")) {
		return ddlEffect{}
	}

	var eff ddlEffect

	switch {
	case toks[0].is(kwCreate):
		eff.action = actionCreateDatabase
	case toks[0].is(kwDrop):
		eff.action = actionDropDatabase
	default:
		return ddlEffect{}
	}

	rest := toks[ddlHeadTokens:]
	rest, eff.ifClause = skipIfClause(rest, eff.action == actionCreateDatabase)

	if len(rest) == 0 {
		eff.syntaxErr = "database name is required"

		return eff
	}

	eff.database = strings.ToLower(rest[0].text)

	if eff.action == actionCreateDatabase {
		eff.syntaxErr = parseCreateDatabaseClauses(rest[1:], &eff)
	} else {
		eff.syntaxErr = parseDropDatabaseClauses(rest[1:], &eff)
	}

	return eff
}

// skipIfClause strips IF NOT EXISTS (create) or IF EXISTS (drop).
func skipIfClause(toks []ddlToken, create bool) ([]ddlToken, bool) {
	want := []string{"IF", "EXISTS"}
	if create {
		want = []string{"IF", "NOT", "EXISTS"}
	}

	if len(toks) < len(want) {
		return toks, false
	}

	for i, kw := range want {
		if !toks[i].is(kw) {
			return toks, false
		}
	}

	return toks[len(want):], true
}

// parseCreateDatabaseClauses reads COMMENT, LOCATION and WITH DBPROPERTIES.
// It returns a syntax error message, or "" on success.
func parseCreateDatabaseClauses(toks []ddlToken, eff *ddlEffect) string {
	for len(toks) > 0 {
		if v, rest, ok := literalClause(toks, "COMMENT"); ok {
			eff.comment, toks = v, rest

			continue
		}

		if v, rest, ok := literalClause(toks, "LOCATION"); ok {
			eff.location, toks = v, rest

			continue
		}

		if len(toks) < ddlHeadTokens || !toks[0].is("WITH") || !toks[1].is("DBPROPERTIES") {
			return "unexpected token '" + toks[0].text + "'"
		}

		props, rest, ok := parseProperties(toks[ddlHeadTokens:])
		if !ok {
			return "malformed DBPROPERTIES clause"
		}

		eff.properties, toks = props, rest
	}

	return ""
}

// literalClause reads "<kw> '<literal>'" and returns the literal and the
// tokens after it.
func literalClause(toks []ddlToken, kw string) (value string, rest []ddlToken, ok bool) {
	if len(toks) < ddlHeadTokens || !toks[0].is(kw) || !toks[1].quoted {
		return "", toks, false
	}

	return toks[1].text, toks[ddlHeadTokens:], true
}

// parseDropDatabaseClauses reads an optional RESTRICT or CASCADE.
func parseDropDatabaseClauses(toks []ddlToken, eff *ddlEffect) string {
	switch {
	case len(toks) == 0:
		return ""
	case len(toks) == 1 && toks[0].is("CASCADE"):
		eff.cascade = true

		return ""
	case len(toks) == 1 && toks[0].is("RESTRICT"):
		return ""
	default:
		return "unexpected token '" + toks[0].text + "'"
	}
}

// parseProperties reads ('k'='v', ...) and returns the tokens after it.
func parseProperties(toks []ddlToken) (map[string]string, []ddlToken, bool) {
	const pairTokens = 3 // 'k' = 'v'

	if len(toks) == 0 || !toks[0].is("(") {
		return nil, nil, false
	}

	props := map[string]string{}
	toks = toks[1:]

	for len(toks) >= pairTokens+1 {
		if !toks[0].quoted || !toks[1].is("=") || !toks[2].quoted {
			return nil, nil, false
		}

		props[toks[0].text] = toks[2].text
		sep := toks[pairTokens]
		toks = toks[pairTokens+1:]

		if sep.is(")") {
			return props, toks, true
		}

		if !sep.is(",") {
			return nil, nil, false
		}
	}

	return nil, nil, false
}

// lexDDL splits a statement into words, quoted literals and the punctuation
// ( ) , = . A trailing semicolon is dropped. Inside a quoted literal a doubled
// quote stands for one quote.
func lexDDL(query string) []ddlToken {
	var toks []ddlToken

	rs := []rune(strings.TrimRight(strings.TrimSpace(query), ";"))

	for i := 0; i < len(rs); {
		r := rs[i]

		switch {
		case unicode.IsSpace(r):
			i++
		case strings.ContainsRune("(),=", r):
			toks = append(toks, ddlToken{text: string(r)})
			i++
		case r == '\'' || r == '"' || r == '`':
			text, next := readQuoted(rs, i)
			toks = append(toks, ddlToken{text: text, quoted: true})
			i = next
		default:
			j := i
			for j < len(rs) && !unicode.IsSpace(rs[j]) && !strings.ContainsRune("(),='\"`", rs[j]) {
				j++
			}

			toks = append(toks, ddlToken{text: string(rs[i:j])})
			i = j
		}
	}

	return toks
}

// readQuoted reads the literal opened at rs[start] and returns its text and
// the index after the closing quote.
func readQuoted(rs []rune, start int) (text string, next int) {
	quote := rs[start]

	var b strings.Builder

	i := start + 1
	for i < len(rs) {
		if rs[i] == quote {
			if i+1 < len(rs) && rs[i+1] == quote {
				b.WriteRune(quote)

				i += 2

				continue
			}

			return b.String(), i + 1
		}

		b.WriteRune(rs[i])

		i++
	}

	return b.String(), i
}
