package resourcegraph

import "testing"

// TestSQLVirtualMachineTypeMapping locks the #913 ARG-triple: the SQL VM overlay
// maps both ways between the portable compute/SqlVirtualMachine pair and the real
// ARM type microsoft.sqlvirtualmachine/sqlvirtualmachines.
func TestSQLVirtualMachineTypeMapping(t *testing.T) {
	const armSQLVM = "microsoft.sqlvirtualmachine/sqlvirtualmachines"

	// Reverse: a discovered overlay row stamps the real ARM type.
	if got := portableToAzureType("compute", "SqlVirtualMachine"); got != armSQLVM {
		t.Errorf("portableToAzureType(compute,SqlVirtualMachine) = %q, want %q", got, armSQLVM)
	}
}
