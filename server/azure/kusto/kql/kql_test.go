package kql_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/server/azure/kusto/kql"
)

func TestParseColumnType(t *testing.T) {
	cases := []struct {
		in   string
		want kql.ColumnType
		ok   bool
	}{
		{"long", kql.TypeLong, true},
		{"Int64", kql.TypeLong, true},
		{"System.Int64", kql.TypeLong, true},
		{"int", kql.TypeInt, true},
		{"string", kql.TypeString, true},
		{"  REAL ", kql.TypeReal, true},
		{"boolean", kql.TypeBool, true},
		{"datetime", kql.TypeDateTime, true},
		{"dynamic", kql.TypeDynamic, true},
		{"guid", kql.TypeGUID, true},
		{"nope", "", false},
	}

	for _, c := range cases {
		got, ok := kql.ParseColumnType(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("ParseColumnType(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestColumnTypeDataType(t *testing.T) {
	cases := map[kql.ColumnType]string{
		kql.TypeString:   "String",
		kql.TypeLong:     "Int64",
		kql.TypeInt:      "Int32",
		kql.TypeReal:     "Double",
		kql.TypeBool:     "Boolean",
		kql.TypeDateTime: "DateTime",
		kql.TypeDynamic:  "Object",
		kql.TypeGUID:     "Guid",
	}

	for typ, want := range cases {
		if got := typ.DataType(); got != want {
			t.Errorf("%q.DataType() = %q, want %q", typ, got, want)
		}
	}
}

func TestParseColumnList(t *testing.T) {
	cols, err := kql.ParseColumnList("(id:long, name:string, ts:datetime)")
	if err != nil {
		t.Fatalf("ParseColumnList: %v", err)
	}

	want := []kql.Column{
		{Name: "id", Type: kql.TypeLong},
		{Name: "name", Type: kql.TypeString},
		{Name: "ts", Type: kql.TypeDateTime},
	}

	if len(cols) != len(want) {
		t.Fatalf("got %d columns, want %d", len(cols), len(want))
	}

	for i := range want {
		if cols[i] != want[i] {
			t.Errorf("col[%d] = %+v, want %+v", i, cols[i], want[i])
		}
	}
}

func TestParseColumnListNoParens(t *testing.T) {
	cols, err := kql.ParseColumnList("id:long, name:string")
	if err != nil {
		t.Fatalf("ParseColumnList without parens: %v", err)
	}

	if len(cols) != 2 {
		t.Fatalf("got %d columns, want 2", len(cols))
	}
}

func TestParseColumnListErrors(t *testing.T) {
	cases := []string{
		"()",
		"(id)",
		"(id:bogustype)",
		"(:long)",
		"(id:long, id:string)",
	}

	for _, c := range cases {
		if _, err := kql.ParseColumnList(c); err == nil {
			t.Errorf("ParseColumnList(%q) = nil error, want error", c)
		}
	}
}

func TestCSLSchema(t *testing.T) {
	cols := []kql.Column{
		{Name: "id", Type: kql.TypeLong},
		{Name: "name", Type: kql.TypeString},
	}

	if got := kql.CSLSchema(cols); got != "id:long,name:string" {
		t.Errorf("CSLSchema = %q, want %q", got, "id:long,name:string")
	}
}
