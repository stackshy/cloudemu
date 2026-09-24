package nosql

import (
	"regexp"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/database/driver"
)

// DDL statement kinds the parser recognizes.
const (
	DDLCreateTable = "CREATE TABLE"
	DDLAlterTable  = "ALTER TABLE"
)

// maxPrimaryKeyColumns is what the portable partition/sort key pair holds. A
// third column would make two distinct rows share one identity, so the parser
// refuses rather than silently collapsing them.
const maxPrimaryKeyColumns = 2

// Column types the mock stores values for.
const (
	typeInteger   = "INTEGER"
	typeLong      = "LONG"
	typeFloat     = "FLOAT"
	typeDouble    = "DOUBLE"
	typeNumber    = "NUMBER"
	typeString    = "STRING"
	typeBoolean   = "BOOLEAN"
	typeBinary    = "BINARY"
	typeTimestamp = "TIMESTAMP"
	typeJSON      = "JSON"
)

// columnTypes are the scalar OCI types the mock stores. The structured types
// (ARRAY, MAP, RECORD, ENUM) and generated columns are rejected by name.
//
//nolint:gochecknoglobals // a lookup table, read-only after init.
var columnTypes = map[string]bool{
	typeInteger: true, typeLong: true, typeFloat: true, typeDouble: true,
	typeNumber: true, typeString: true, typeBoolean: true, typeBinary: true,
	typeTimestamp: true, typeJSON: true,
}

// identifierRE is an OCI NoSQL table, column or index name. The leading
// letter is what keeps a declared column from colliding with ttlExpiryColumn.
var identifierRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// AlterSpec is what an ALTER TABLE statement changes.
type AlterSpec struct {
	AddColumns  []Column
	DropColumns []string
	TTL         *TTL
}

// IndexSpec describes an index to build.
type IndexSpec struct {
	Name    string
	Table   string
	Columns []string
}

// DDL is a parsed statement. Only the fields its Kind uses are populated.
type DDL struct {
	Kind        string
	Table       string
	IfNotExists bool
	Schema      Schema
	Alter       AlterSpec
}

// ParseDDL parses the statement OCI's CreateTable and UpdateTable take. It
// rejects, by name, anything it does not model rather than accepting a
// statement it would then ignore.
func ParseDDL(statement string) (*DDL, error) {
	stmt := normaliseStatement(statement)
	if stmt == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "ddlStatement is required")
	}

	upper := strings.ToUpper(stmt)

	switch {
	case strings.HasPrefix(upper, DDLCreateTable):
		return parseCreateTable(stmt)
	case strings.HasPrefix(upper, DDLAlterTable):
		return parseAlterTable(stmt)
	}

	return nil, cerrors.Newf(cerrors.InvalidArgument,
		"unsupported DDL statement %q; CloudEmu parses CREATE TABLE and ALTER TABLE. Indexes are built "+
			"through the indexes endpoint and a table is dropped through DeleteTable", leadingWords(stmt))
}

// normaliseStatement collapses whitespace and drops a trailing semicolon.
func normaliseStatement(statement string) string {
	return strings.TrimSuffix(strings.Join(strings.Fields(statement), " "), ";")
}

// leadingWords names the statement a rejection is about, without echoing a
// whole multi-line body back at the caller.
func leadingWords(stmt string) string {
	words := strings.Fields(stmt)
	if len(words) > 2 { //nolint:mnd // the verb and its object are enough to name it
		words = words[:2]
	}

	return strings.Join(words, " ")
}

// parseCreateTable parses
// CREATE TABLE [IF NOT EXISTS] name (columns, PRIMARY KEY (...)) [USING TTL n DAYS].
func parseCreateTable(stmt string) (*DDL, error) {
	rest := stmt[len(DDLCreateTable):]

	d := &DDL{Kind: DDLCreateTable}
	rest, d.IfNotExists = cutKeyword(rest, "IF NOT EXISTS")

	head, body, tail, err := splitParenthesised(rest)
	if err != nil {
		return nil, err
	}

	if d.Table, err = requireIdentifier(head, "table name"); err != nil {
		return nil, err
	}

	if d.Schema, err = parseSchema(body); err != nil {
		return nil, err
	}

	ttl, err := parseTTLClause(tail)
	if err != nil {
		return nil, err
	}

	if ttl != nil {
		d.Schema.TTL = *ttl
	}

	return d, nil
}

// parseSchema reads the column list and the PRIMARY KEY declaration.
func parseSchema(body string) (Schema, error) {
	fields := splitTopLevel(body)
	if len(fields) == 0 {
		return Schema{}, cerrors.New(cerrors.InvalidArgument, "CREATE TABLE declares no columns")
	}

	var (
		s      Schema
		keyed  bool
		keyErr error
	)

	for _, f := range fields {
		if strings.HasPrefix(strings.ToUpper(f), "PRIMARY KEY") {
			if keyed {
				return Schema{}, cerrors.New(cerrors.InvalidArgument, "CREATE TABLE declares PRIMARY KEY twice")
			}

			keyed = true

			if s.ShardKey, s.PrimaryKey, keyErr = parsePrimaryKey(f); keyErr != nil {
				return Schema{}, keyErr
			}

			continue
		}

		col, err := parseColumn(f)
		if err != nil {
			return Schema{}, err
		}

		s.Columns = append(s.Columns, col)
	}

	if !keyed {
		return Schema{}, cerrors.New(cerrors.InvalidArgument, "CREATE TABLE declares no PRIMARY KEY")
	}

	if err := validateSchema(&s); err != nil {
		return Schema{}, err
	}

	return s, nil
}

// validateSchema checks that every key column is declared and that the
// primary key fits the portable partition/sort key pair.
func validateSchema(s *Schema) error {
	declared := map[string]bool{}
	for _, c := range s.Columns {
		declared[c.Name] = true
	}

	for _, k := range s.PrimaryKey {
		if !declared[k] {
			return cerrors.Newf(cerrors.InvalidArgument, "primary key column %q is not declared", k)
		}
	}

	if len(s.PrimaryKey) > maxPrimaryKeyColumns {
		return cerrors.Newf(cerrors.InvalidArgument,
			"primary keys of more than %d columns are not supported; CloudEmu maps the OCI primary key "+
				"onto the portable partition and sort key pair", maxPrimaryKeyColumns)
	}

	if len(s.ShardKey) != 1 {
		return cerrors.New(cerrors.InvalidArgument,
			"composite shard keys are not supported; declare SHARD over exactly one column")
	}

	if s.ShardKey[0] != s.PrimaryKey[0] {
		return cerrors.New(cerrors.InvalidArgument, "the shard key must be the leading primary key column")
	}

	return nil
}

// parsePrimaryKey reads PRIMARY KEY (SHARD(a), b) or PRIMARY KEY (a, b),
// returning the shard columns and the full key in declaration order.
func parsePrimaryKey(field string) (shard, full []string, err error) {
	inner, err := insideParens(field, "PRIMARY KEY")
	if err != nil {
		return nil, nil, err
	}

	for _, part := range splitTopLevel(inner) {
		if !strings.HasPrefix(strings.ToUpper(part), "SHARD") {
			name, idErr := requireIdentifier(part, "primary key column")
			if idErr != nil {
				return nil, nil, idErr
			}

			full = append(full, name)

			continue
		}

		if len(full) > 0 {
			return nil, nil, cerrors.New(cerrors.InvalidArgument, "SHARD must lead the PRIMARY KEY")
		}

		columns, sErr := parseShardGroup(part)
		if sErr != nil {
			return nil, nil, sErr
		}

		shard = append(shard, columns...)
		full = append(full, columns...)
	}

	if len(full) == 0 {
		return nil, nil, cerrors.New(cerrors.InvalidArgument, "PRIMARY KEY names no columns")
	}

	// Without an explicit SHARD, OCI shards on the leading primary key column.
	if len(shard) == 0 {
		shard = []string{full[0]}
	}

	return shard, full, nil
}

// parseShardGroup reads the column list inside SHARD(...).
func parseShardGroup(part string) ([]string, error) {
	inner, err := insideParens(part, "SHARD")
	if err != nil {
		return nil, err
	}

	var out []string

	for _, c := range splitTopLevel(inner) {
		name, idErr := requireIdentifier(c, "shard key column")
		if idErr != nil {
			return nil, idErr
		}

		out = append(out, name)
	}

	return out, nil
}

// parseColumn reads NAME TYPE [NOT NULL] [DEFAULT value].
func parseColumn(field string) (Column, error) {
	words := strings.Fields(field)
	if len(words) < 2 { //nolint:mnd // a column is at least a name and a type
		return Column{}, cerrors.Newf(cerrors.InvalidArgument, "column %q declares no type", field)
	}

	name, err := requireIdentifier(words[0], "column name")
	if err != nil {
		return Column{}, err
	}

	typeName, err := parseColumnType(words[1])
	if err != nil {
		return Column{}, err
	}

	col := Column{Name: name, Type: typeName, IsNullable: true}

	if err := parseColumnModifiers(&col, words[2:]); err != nil {
		return Column{}, err
	}

	return col, nil
}

// parseColumnType strips a TIMESTAMP precision and rejects the types the mock
// has no storage shape for.
func parseColumnType(word string) (string, error) {
	name := strings.ToUpper(word)
	if i := strings.IndexByte(name, '('); i >= 0 {
		name = name[:i]
	}

	if !columnTypes[name] {
		return "", cerrors.Newf(cerrors.InvalidArgument,
			"column type %q is not supported; CloudEmu stores the scalar types and JSON", name)
	}

	return name, nil
}

// parseColumnModifiers applies NOT NULL and DEFAULT. Anything else — the
// generated, counter and comment modifiers among them — is named and refused.
func parseColumnModifiers(col *Column, words []string) error {
	for i := 0; i < len(words); i++ {
		switch {
		case strings.EqualFold(words[i], "NOT") && i+1 < len(words) && strings.EqualFold(words[i+1], "NULL"):
			col.IsNullable = false
			i++
		case strings.EqualFold(words[i], "DEFAULT"):
			if i+1 >= len(words) {
				return cerrors.Newf(cerrors.InvalidArgument, "DEFAULT on column %q names no value", col.Name)
			}

			col.DefaultValue = strings.Trim(strings.Join(words[i+1:], " "), `"'`)

			return nil
		default:
			return cerrors.Newf(cerrors.InvalidArgument,
				"unsupported column modifier %q on column %q", words[i], col.Name)
		}
	}

	return nil
}

// parseAlterTable parses ALTER TABLE name (ADD col type | DROP col) and
// ALTER TABLE name USING TTL n DAYS.
func parseAlterTable(stmt string) (*DDL, error) {
	rest := stmt[len(DDLAlterTable):]
	d := &DDL{Kind: DDLAlterTable}

	if i := strings.IndexByte(rest, '('); i < 0 {
		return parseAlterTTL(d, rest)
	}

	head, body, tail, err := splitParenthesised(rest)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(tail) != "" {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "unsupported ALTER TABLE clause %q", strings.TrimSpace(tail))
	}

	if d.Table, err = requireIdentifier(head, "table name"); err != nil {
		return nil, err
	}

	for _, part := range splitTopLevel(body) {
		if err := applyAlterPart(&d.Alter, part); err != nil {
			return nil, err
		}
	}

	return d, nil
}

// applyAlterPart reads one ADD or DROP inside an ALTER TABLE body.
func applyAlterPart(spec *AlterSpec, part string) error {
	upper := strings.ToUpper(part)

	switch {
	case strings.HasPrefix(upper, "ADD "):
		col, err := parseColumn(strings.TrimSpace(part[len("ADD "):]))
		if err != nil {
			return err
		}

		spec.AddColumns = append(spec.AddColumns, col)

		return nil
	case strings.HasPrefix(upper, "DROP "):
		name, err := requireIdentifier(part[len("DROP "):], "column name")
		if err != nil {
			return err
		}

		spec.DropColumns = append(spec.DropColumns, name)

		return nil
	}

	return cerrors.Newf(cerrors.InvalidArgument,
		"unsupported ALTER TABLE action %q; CloudEmu applies ADD, DROP and USING TTL", leadingWords(part))
}

// parseAlterTTL reads the parenthesis-free forms of ALTER TABLE.
func parseAlterTTL(d *DDL, rest string) (*DDL, error) {
	words := strings.Fields(rest)
	if len(words) == 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "ALTER TABLE names no table")
	}

	name, err := requireIdentifier(words[0], "table name")
	if err != nil {
		return nil, err
	}

	d.Table = name

	clause := strings.Join(words[1:], " ")
	if !strings.HasPrefix(strings.ToUpper(clause), "USING ") {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"unsupported ALTER TABLE clause %q; CloudEmu applies ADD, DROP and USING TTL", clause)
	}

	ttl, err := parseTTLClause(clause)
	if err != nil {
		return nil, err
	}

	d.Alter.TTL = ttl

	return d, nil
}

// parseTTLClause reads a trailing USING TTL n DAYS|HOURS. An empty clause
// returns nil; anything else trailing is an error, so a clause the mock does
// not model is never accepted and ignored.
func parseTTLClause(clause string) (*TTL, error) {
	words := strings.Fields(clause)
	if len(words) == 0 {
		return nil, nil //nolint:nilnil // no clause is not an error
	}

	const ttlWords = 4 // USING TTL n UNIT

	if len(words) != ttlWords || !strings.EqualFold(words[0], "USING") || !strings.EqualFold(words[1], "TTL") {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"unsupported clause %q; CloudEmu applies USING TTL <n> DAYS", clause)
	}

	n, err := strconv.Atoi(words[2])
	if err != nil || n < 0 {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "TTL value %q is not a non-negative integer", words[2])
	}

	if !strings.EqualFold(words[3], "DAYS") {
		return nil, cerrors.Newf(cerrors.InvalidArgument,
			"TTL unit %q is not supported; OCI reports a table's TTL in DAYS", words[3])
	}

	return &TTL{Days: n}, nil
}

// cutKeyword strips a leading keyword phrase, reporting whether it was there.
func cutKeyword(s, keyword string) (rest string, found bool) {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) < len(keyword) || !strings.EqualFold(trimmed[:len(keyword)], keyword) {
		return s, false
	}

	return trimmed[len(keyword):], true
}

// splitParenthesised splits "head ( body ) tail" on the outermost pair.
func splitParenthesised(s string) (head, body, tail string, err error) {
	open := strings.IndexByte(s, '(')
	if open < 0 {
		return "", "", "", cerrors.Newf(cerrors.InvalidArgument, "statement %q has no parenthesised body", leadingWords(s))
	}

	depth := 0

	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--

			if depth == 0 {
				return s[:open], s[open+1 : i], s[i+1:], nil
			}
		}
	}

	return "", "", "", cerrors.New(cerrors.InvalidArgument, "unbalanced parentheses in DDL statement")
}

// splitTopLevel splits a comma list, ignoring commas nested in parentheses.
func splitTopLevel(s string) []string {
	var (
		out   []string
		depth int
		start int
	)

	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = appendField(out, s[start:i])
				start = i + 1
			}
		}
	}

	return appendField(out, s[start:])
}

func appendField(out []string, field string) []string {
	if f := strings.TrimSpace(field); f != "" {
		out = append(out, f)
	}

	return out
}

// insideParens returns the body of "keyword ( ... )".
func insideParens(s, keyword string) (string, error) {
	_, body, tail, err := splitParenthesised(s)
	if err != nil {
		return "", err
	}

	if strings.TrimSpace(tail) != "" {
		return "", cerrors.Newf(cerrors.InvalidArgument, "unexpected %q after %s", strings.TrimSpace(tail), keyword)
	}

	return body, nil
}

// requireIdentifier validates a table, column or index name.
func requireIdentifier(s, what string) (string, error) {
	name := strings.TrimSpace(s)
	if !identifierRE.MatchString(name) {
		return "", cerrors.Newf(cerrors.InvalidArgument, "%s %q is not a valid identifier", what, name)
	}

	return name, nil
}

// schemaFromConfig derives the OCI schema a portable table config implies.
// The portable shape declares no types, so every column is a STRING.
func schemaFromConfig(cfg *driver.TableConfig) Schema {
	s := Schema{
		Columns:    []Column{{Name: cfg.PartitionKey, Type: typeString}},
		PrimaryKey: []string{cfg.PartitionKey},
		ShardKey:   []string{cfg.PartitionKey},
	}

	if cfg.SortKey != "" {
		s.Columns = append(s.Columns, Column{Name: cfg.SortKey, Type: typeString})
		s.PrimaryKey = append(s.PrimaryKey, cfg.SortKey)
	}

	return s
}

// ddlFromSchema renders the CREATE TABLE statement a schema corresponds to,
// so a table created through the portable API still reports one.
func ddlFromSchema(name string, s *Schema) string {
	cols := make([]string, 0, len(s.Columns))
	for _, c := range s.Columns {
		cols = append(cols, c.Name+" "+c.Type)
	}

	key := "SHARD(" + strings.Join(s.ShardKey, ", ") + ")"
	if sk := sortKeyOf(s); sk != "" {
		key += ", " + sk
	}

	stmt := "CREATE TABLE " + name + " (" + strings.Join(cols, ", ") + ", PRIMARY KEY (" + key + "))"

	if s.TTL.Days > 0 {
		stmt += " USING TTL " + strconv.Itoa(s.TTL.Days) + " DAYS"
	}

	return stmt
}

// indexFromGSI projects a portable index config onto the OCI shape.
func indexFromGSI(cfg *driver.GSIConfig) IndexSpec {
	spec := IndexSpec{Name: cfg.Name, Columns: []string{cfg.PartitionKey}}
	if cfg.SortKey != "" {
		spec.Columns = append(spec.Columns, cfg.SortKey)
	}

	return spec
}
