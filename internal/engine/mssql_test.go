package engine

import (
	"errors"
	"testing"
)

func TestMSSQLReadOnlyBatch(t *testing.T) {
	ms, _ := GetDriver("mssql")
	odbc := odbcDriver{dialect: "mssql"}

	tests := []struct {
		name    string
		sql     string
		wantErr bool
		risk    Risk
	}{
		{"batch commit", "SELECT 1 COMMIT DELETE FROM t", true, RiskWrite},
		{"batch drop", "select 1 drop table x", true, RiskDDL},
		{"batch set", "select 1 set implicit_transactions off", true, RiskWrite},
		{"batch insert", "select 1\ninsert t values (1)", true, RiskWrite},
		{"select into", "select * into t2 from t", true, RiskWrite},
		{"exec", "select 1 exec sp_who", true, RiskWrite},
		{"literals and comments", "select 'into', N'delete' as [drop], \"exec\" -- update\n from t /* insert */", false, RiskRead},
		{"variables and temp tables", "select @update = 1, #save.x from #save", false, RiskRead},
		{"plain select", "select top 10 * from [dbo].[t]", false, RiskRead},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, d := range []Driver{ms, odbc} {
				_, err := Validate(d, tt.sql, true)
				if (err != nil) != tt.wantErr {
					t.Fatalf("%s Validate() error = %v, wantErr %v", d.Type(), err, tt.wantErr)
				}

				if tt.wantErr && !errors.Is(err, ErrReadOnly) {
					t.Fatalf("%s Validate() error = %v, want ErrReadOnly", d.Type(), err)
				}

				if got := Classify(d, tt.sql); got != tt.risk {
					t.Errorf("%s Classify(%q) = %s, want %s", d.Type(), tt.sql, got, tt.risk)
				}
			}
		})
	}
}
