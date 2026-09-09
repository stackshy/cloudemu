package athena

import (
	"strings"

	"github.com/stackshy/cloudemu/v2/services/athena/driver"
)

// kwCreate and kwDrop are SQL keywords shared across the classifier and the DDL
// parser.
const (
	kwCreate = "CREATE"
	kwDrop   = "DROP"
)

// minDatabaseDDLTokens is the fewest whitespace-separated tokens a recognizable
// CREATE/DROP DATABASE statement can have ("CREATE DATABASE name").
const minDatabaseDDLTokens = 3

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

// ddlEffect is the catalog mutation a DDL statement performs.
type ddlEffect struct {
	action   string // "createDatabase" | "dropDatabase" | ""
	database string
	ifClause bool // IF NOT EXISTS (create) / IF EXISTS (drop)
}

// parseDatabaseDDL recognizes CREATE/DROP DATABASE (or SCHEMA) statements and
// returns the catalog effect. Any other statement yields an empty action, which
// the executor treats as a no-op that still settles SUCCEEDED (there is no real
// compute plane behind non-database DDL).
func parseDatabaseDDL(query string) ddlEffect {
	tokens := strings.Fields(query)
	if len(tokens) < minDatabaseDDLTokens {
		return ddlEffect{}
	}

	verb := strings.ToUpper(tokens[0])
	object := strings.ToUpper(tokens[1])

	if object != "DATABASE" && object != "SCHEMA" {
		return ddlEffect{}
	}

	switch verb {
	case kwCreate:
		name, ifClause := databaseNameFrom(tokens[2:])

		return ddlEffect{action: "createDatabase", database: name, ifClause: ifClause}
	case kwDrop:
		name, ifClause := databaseNameFrom(tokens[2:])

		return ddlEffect{action: "dropDatabase", database: name, ifClause: ifClause}
	default:
		return ddlEffect{}
	}
}

// databaseNameFrom extracts the database name from the tokens following
// CREATE/DROP DATABASE, skipping an optional "IF NOT EXISTS" / "IF EXISTS"
// clause (everything up to and including EXISTS) and unquoting the name.
func databaseNameFrom(rest []string) (string, bool) {
	ifClause := false

	if len(rest) > 0 && strings.EqualFold(rest[0], "IF") {
		ifClause = true

		i := 0
		for i < len(rest) {
			word := strings.ToUpper(rest[i])
			i++

			if word == "EXISTS" {
				break
			}
		}

		rest = rest[i:]
	}

	if len(rest) == 0 {
		return "", ifClause
	}

	return unquoteIdentifier(rest[0]), ifClause
}

// unquoteIdentifier strips surrounding backticks, double quotes, or single
// quotes and any trailing punctuation from a SQL identifier token.
func unquoteIdentifier(s string) string {
	s = strings.TrimRight(s, ";")
	s = strings.Trim(s, "`\"'")

	return s
}
