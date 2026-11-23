package engine

import (
	"testing"
	"time"
)

func TestTimeLiteral(t *testing.T) {
	ora, _ := GetDriver("oracle")
	ms, _ := GetDriver("mssql")
	ts := time.Date(2026, 3, 4, 5, 6, 7, 123456700, time.UTC)

	tests := []struct {
		d    Driver
		typ  string
		want string
	}{
		{ora, "date", "TO_DATE('2026-03-04 05:06:07', 'YYYY-MM-DD HH24:MI:SS')"},
		{ms, "datetime2", "'2026-03-04T05:06:07.1234567'"},
		{ms, "datetime", "'2026-03-04T05:06:07.123'"},
	}

	for _, tt := range tests {
		if got := timeLiteral(tt.d, tt.typ, ts); got != tt.want {
			t.Errorf("%s %s: %s, want %s", tt.d.Type(), tt.typ, got, tt.want)
		}
	}
}

// Values a transfer reads go over as the target can take them.
func TestSourceValue(t *testing.T) {
	duck, _ := GetDriver("duckdb")
	uuid := []byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0}

	if got := sourceValue("uuid", converterFor(duck), uuid); got != "12345678-9abc-def0-1234-56789abcdef0" {
		t.Errorf("duckdb uuid: %#v", got)
	}

	// MySQL BIT: one bit is a flag, wider a number.
	if got := sourceValue("bit", nil, []byte{1}); got != true {
		t.Errorf("bit(1): %#v", got)
	}

	if got := sourceValue("bit", nil, []byte{0x01, 0x02}); got != int64(258) {
		t.Errorf("bit(16): %#v", got)
	}

	// Oracle RAW stays bytes, text columns become text.
	if got, ok := sourceValue("raw", nil, []byte{0xff}).([]byte); !ok || got[0] != 0xff {
		t.Errorf("raw: %#v", got)
	}

	if got := sourceValue("varchar", nil, []byte("abc")); got != "abc" {
		t.Errorf("varchar: %#v", got)
	}
}
