package resourceexplorer2

import (
	"slices"
	"testing"
)

// A service:<awsname> filter must map back to the portable Service the walkers
// emit, or the filter matches nothing. This guards the inbound map against
// drift from the outbound portableToAWSService map.
func TestAWSToPortableServicesRoundTrip(t *testing.T) {
	cases := map[string]string{
		awsServiceSecrets:  portableServiceSecrets,
		awsServiceECR:      portableServiceContainer,
		awsServiceSQS:      portableServiceQueue,
		awsServiceSNS:      portableServiceNotif,
		awsServiceRoute53:  portableServiceDNS,
		awsServiceLogs:     portableServiceLogging,
		awsServiceCache:    portableServiceCache,
		awsServiceELB:      portableServiceLB,
		awsServiceCW:       portableServiceMonitoring,
		awsServiceS3:       portableServiceStorage,
		awsServiceDynamoDB: portableServiceDatabase,
		awsServiceLambda:   portableServiceServerless,
		awsServiceRDS:      portableServiceRelationalDB,
	}

	for awsName, portable := range cases {
		got := awsToPortableServices(awsName)
		if !slices.Contains(got, portable) {
			t.Errorf("service:%s → %v, want it to include portable %q", awsName, got, portable)
		}

		// And the outbound map must round-trip the portable name back to the
		// same AWS name, so display and filter stay symmetric.
		if back := portableToAWSService(portable); back != awsName {
			t.Errorf("portableToAWSService(%q) = %q, want %q", portable, back, awsName)
		}
	}

	// ec2 fans out to both compute and networking.
	if got := awsToPortableServices(awsServiceEC2); !slices.Contains(got, portableServiceCompute) ||
		!slices.Contains(got, portableServiceNetworking) {
		t.Errorf("service:ec2 → %v, want compute+networking", got)
	}
}
