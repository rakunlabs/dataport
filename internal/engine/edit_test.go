package engine

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestEditSQL(t *testing.T) {
	pg, _ := GetDriver("postgres")
	ms, _ := GetDriver("mssql")

	cols := map[string]ColumnInfo{
		"id":   {Name: "id", PrimaryKey: true},
		"name": {Name: "name"},
		"city": {Name: "city"},
	}
	pk := []string{"id"}

	tests := []struct {
		name     string
		d        Driver
		edit     RowEdit
		wantSQL  string
		wantArgs int
		wantErr  bool
	}{
		{
			name:     "update",
			d:        pg,
			edit:     RowEdit{Op: "update", Key: map[string]any{"id": 1}, Values: map[string]any{"name": "a", "city": nil}},
			wantSQL:  `UPDATE "public"."t" SET "city" = $1, "name" = $2 WHERE "id" = $3`,
			wantArgs: 3,
		},
		{
			name:     "delete mssql",
			d:        ms,
			edit:     RowEdit{Op: "delete", Key: map[string]any{"id": 7}},
			wantSQL:  `DELETE FROM "public"."t" WHERE [id] = @p1`,
			wantArgs: 1,
		},
		{
			name:     "insert",
			d:        pg,
			edit:     RowEdit{Op: "insert", Values: map[string]any{"name": "x"}},
			wantSQL:  `INSERT INTO "public"."t" ("name") VALUES ($1)`,
			wantArgs: 1,
		},
		{"defaults only", pg, RowEdit{Op: "insert"}, `INSERT INTO "public"."t" DEFAULT VALUES`, 0, false},
		{"unknown column", pg, RowEdit{Op: "update", Key: map[string]any{"id": 1}, Values: map[string]any{"x; drop": 1}}, "", 0, true},
		{"missing key", pg, RowEdit{Op: "delete", Key: map[string]any{}}, "", 0, true},
		{"null key", pg, RowEdit{Op: "delete", Key: map[string]any{"id": nil}}, "", 0, true},
		{"extra key", pg, RowEdit{Op: "delete", Key: map[string]any{"id": 1, "name": "a"}}, "", 0, true},
		{"empty update", pg, RowEdit{Op: "update", Key: map[string]any{"id": 1}}, "", 0, true},
		{"bad op", pg, RowEdit{Op: "truncate"}, "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, args, err := editSQL(tt.d, `"public"."t"`, cols, pk, false, tt.edit)
			if tt.wantErr {
				if !errors.Is(err, ErrBadEdit) {
					t.Fatalf("err = %v, want ErrBadEdit", err)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if got != tt.wantSQL || len(args) != tt.wantArgs {
				t.Fatalf("got %q %v, want %q with %d args", got, args, tt.wantSQL, tt.wantArgs)
			}
		})
	}
}

func TestEditSQLVirtualKey(t *testing.T) {
	pg, _ := GetDriver("postgres")

	cols := map[string]ColumnInfo{"name": {Name: "name"}, "city": {Name: "city"}}

	got, args, err := editSQL(pg, `"t"`, cols, []string{"name", "city"}, true, RowEdit{Op: "delete", Key: map[string]any{"name": "a", "city": nil}})
	if err != nil {
		t.Fatal(err)
	}

	if want := `DELETE FROM "t" WHERE "name" = $1 AND "city" IS NULL`; got != want || len(args) != 1 {
		t.Fatalf("got %q %v, want %q", got, args, want)
	}

	my, _ := GetDriver("mysql")

	got, _, err = editSQL(my, "`t`", cols, []string{"name"}, true, RowEdit{Op: "update", Key: map[string]any{"name": "a"}, Values: map[string]any{"city": "x"}})
	if err != nil {
		t.Fatal(err)
	}

	if want := "UPDATE `t` SET `city` = ? WHERE `name` = ? LIMIT 2"; got != want {
		t.Fatalf("mysql: got %q, want %q", got, want)
	}
}

func TestMarkAuto(t *testing.T) {
	list := []ColumnInfo{
		{Name: "id", Type: "integer", Default: "nextval('t_id_seq'::regclass)", PrimaryKey: true},
		{Name: "n", Type: "int", Extra: "AUTO_INCREMENT"},
		{Name: "Total", Type: "int"},
		{Name: "at", Type: "timestamp", Default: "now()"},
	}
	markAuto("postgres", list, map[string]string{"total": "generated"})

	got := []string{list[0].Auto, list[1].Auto, list[2].Auto, list[3].Auto}
	if want := []string{"identity", "identity", "generated", ""}; !slices.Equal(got, want) {
		t.Fatalf("auto %v, want %v", got, want)
	}
}

// Values the grid sends as text reach binary and Oracle date columns as
// what the database takes; other values stay as they were sent.
func TestEditValues(t *testing.T) {
	pg, _ := GetDriver("postgres")
	ora, _ := GetDriver("oracle")
	my, _ := GetDriver("mysql")

	if got, ok := editValue(my, "blob", `\x00ff`).([]byte); !ok || len(got) != 2 || got[1] != 0xff {
		t.Fatalf("mysql blob: %#v", got)
	}

	if got := editValue(my, "varchar(10)", `\x00ff`); got != `\x00ff` {
		t.Fatalf("text that looks like hex: %#v", got)
	}

	if got, ok := editValue(ora, "TIMESTAMP(6)", "2026-03-04T05:06:07Z").(time.Time); !ok || got.Hour() != 5 {
		t.Fatalf("oracle timestamp: %#v", got)
	}

	if got := editValue(pg, "timestamp", "2026-03-04T05:06:07Z"); got != "2026-03-04T05:06:07Z" {
		t.Fatalf("postgres reads the text itself: %#v", got)
	}

	if got := describe("UPDATE t SET b = ?", []any{[]byte{0, 0xff}}); !strings.Contains(got, `\x00ff`) {
		t.Fatalf("log of bytes: %s", got)
	}
}
