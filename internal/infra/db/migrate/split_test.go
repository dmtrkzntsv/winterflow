package migrate

import (
	"reflect"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := map[string]struct {
		in   string
		want []string
	}{
		"plain":            {"CREATE TABLE a (x INT);\nCREATE TABLE b (y INT);\n", []string{"CREATE TABLE a (x INT)", "CREATE TABLE b (y INT)"}},
		"no trailing ;":    {"DROP TABLE a", []string{"DROP TABLE a"}},
		"single quote":     {"INSERT INTO t VALUES ('a;b');", []string{"INSERT INTO t VALUES ('a;b')"}},
		"escaped quote":    {"INSERT INTO t VALUES ('it''s; ok');", []string{"INSERT INTO t VALUES ('it''s; ok')"}},
		"double quote":     {`CREATE TABLE "a;b" (x INT);`, []string{`CREATE TABLE "a;b" (x INT)`}},
		"line comment":     {"-- drop; everything\nDROP TABLE a;", []string{"-- drop; everything\nDROP TABLE a"}},
		"block comment":    {"/* a; b */ DROP TABLE a;", []string{"/* a; b */ DROP TABLE a"}},
		"comment only":     {"-- nothing to do\n", nil},
		"trailing notes":   {"DROP TABLE a;\n-- done; really\n", []string{"DROP TABLE a"}},
		"empty pieces":     {";;\n;", nil},
		"quote in comment": {"-- don't\nDROP TABLE a;", []string{"-- don't\nDROP TABLE a"}},
	}
	for name, c := range cases {
		if got := Split(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Split(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
}
