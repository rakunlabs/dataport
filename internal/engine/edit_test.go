package engine

import (
	"errors"
	"testing"
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
		{"unknown column", pg, RowEdit{Op: "update", Key: map[string]any{"id": 1}, Values: map[string]any{"x; drop": 1}}, "", 0, true},
		{"missing key", pg, RowEdit{Op: "delete", Key: map[string]any{}}, "", 0, true},
		{"null key", pg, RowEdit{Op: "delete", Key: map[string]any{"id": nil}}, "", 0, true},
		{"extra key", pg, RowEdit{Op: "delete", Key: map[string]any{"id": 1, "name": "a"}}, "", 0, true},
		{"empty update", pg, RowEdit{Op: "update", Key: map[string]any{"id": 1}}, "", 0, true},
		{"bad op", pg, RowEdit{Op: "truncate"}, "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, args, err := editSQL(tt.d, `"public"."t"`, cols, pk, tt.edit)
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
