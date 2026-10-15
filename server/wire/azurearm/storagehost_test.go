package azurearm

import "testing"

func TestStorageHost(t *testing.T) {
	tests := []struct {
		host        string
		wantAccount string
		wantService string
		wantOK      bool
	}{
		{"stbsdk01.blob.core.windows.net", "stbsdk01", "blob", true},
		{"stbsdk01.blob.core.windows.net:4568", "stbsdk01", "blob", true},
		{"StBsdk01.BLOB.Core.Windows.Net:443", "stbsdk01", "blob", true},
		{"acct.queue.core.windows.net", "acct", "queue", true},
		{"acct.table.core.chinacloudapi.cn", "acct", "table", true},
		{"acct.dfs.core.usgovcloudapi.net", "acct", "dfs", true},
		{"acct.file.core.windows.net", "acct", "file", true},
		{"acct.vault.core.windows.net", "", "", false},
		{"blob.core.windows.net", "", "", false},
		{"myvault.vault.azure.net", "", "", false},
		{"localhost:4568", "", "", false},
		{"cloudemu:4568", "", "", false},
		{"", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			account, service, ok := StorageHost(tt.host)
			if account != tt.wantAccount || service != tt.wantService || ok != tt.wantOK {
				t.Fatalf("StorageHost(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.host, account, service, ok, tt.wantAccount, tt.wantService, tt.wantOK)
			}

			if IsStorageHost(tt.host) != tt.wantOK {
				t.Fatalf("IsStorageHost(%q) = %v, want %v", tt.host, !tt.wantOK, tt.wantOK)
			}
		})
	}
}
