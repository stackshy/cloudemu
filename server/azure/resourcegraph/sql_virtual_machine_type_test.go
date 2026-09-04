package resourcegraph

import "testing"

// TestSQLVirtualMachineTypeMapping locks the #913 ARG-triple: the SQL VM overlay
// maps both ways between the portable compute/SqlVirtualMachine pair and the real
// ARM type microsoft.sqlvirtualmachine/sqlvirtualmachines.
func TestSQLVirtualMachineTypeMapping(t *testing.T) {
	const armSQLVM = "microsoft.sqlvirtualmachine/sqlvirtualmachines"

	// Forward: KQL `where type == '<armSQLVM>'` resolves to the overlay pair.
	if svc, typ := mapAzureType(armSQLVM); svc != "compute" || typ != "SqlVirtualMachine" {
		t.Errorf("mapAzureType(%q) = (%q,%q), want (compute,SqlVirtualMachine)", armSQLVM, svc, typ)
	}

	// Reverse: a discovered overlay row stamps the real ARM type.
	if got := portableToAzureType("compute", "SqlVirtualMachine"); got != armSQLVM {
		t.Errorf("portableToAzureType(compute,SqlVirtualMachine) = %q, want %q", got, armSQLVM)
	}
}
