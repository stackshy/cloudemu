package driver

import "testing"

func TestSourceDatabaseRef(t *testing.T) {
	tests := []struct {
		name       string
		id         string
		wantServer string
		wantDB     string
		wantOK     bool
	}{
		{
			name:       "full arm id",
			id:         "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Sql/servers/srv1/databases/db1",
			wantServer: "srv1",
			wantDB:     "db1",
			wantOK:     true,
		},
		{
			name:   "bare name",
			id:     "sourcedb",
			wantOK: false,
		},
		{
			name:   "servers segment only",
			id:     "/providers/Microsoft.Sql/servers/srv1",
			wantOK: false,
		},
		{
			name:   "databases before servers",
			id:     "/databases/db1/servers/srv1",
			wantOK: false,
		},
		{
			name:   "empty",
			id:     "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, db, ok := SourceDatabaseRef(tt.id)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}

			if !ok {
				return
			}

			if server != tt.wantServer || db != tt.wantDB {
				t.Fatalf("got (%q, %q), want (%q, %q)", server, db, tt.wantServer, tt.wantDB)
			}
		})
	}
}
