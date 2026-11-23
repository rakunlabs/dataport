package engine

import "testing"

func TestTinyInt(t *testing.T) {
	for typ, want := range map[string]bool{
		"tinyint": true, "tinyint(4)": true, "tinyint unsigned": true, "Int8": true, "UInt8": true, "Nullable(Int8)": true, "utinyint": true,
		"int8": false, "smallint": false, "Int16": false, "bigint": false,
	} {
		if got := tinyInt(typ); got != want {
			t.Errorf("tinyInt(%q) = %v", typ, got)
		}
	}
}
