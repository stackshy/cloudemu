package mq

import (
	"crypto/sha256"
	"fmt"
	"net"

	"github.com/stackshy/cloudemu/v2/services/mq/driver"
)

// Deployment modes Amazon MQ supports.
const (
	deploymentSingleInstance = "SINGLE_INSTANCE"
	deploymentActiveStandby  = "ACTIVE_STANDBY_MULTI_AZ"
	deploymentClusterMultiAZ = "CLUSTER_MULTI_AZ"
)

// ActiveMQ wire-level ports, emitted in a stable order so the computed
// endpoints list never drifts across reads.
const (
	portOpenWire = 61617
	portAMQP     = 5671
	portSTOMP    = 61614
	portMQTT     = 8883
	portWSS      = 61619
	portConsole  = 8162
)

// activeStandbyInstanceCount is the number of allocated nodes in an
// ACTIVE_STANDBY_MULTI_AZ deployment.
const activeStandbyInstanceCount = 2

// privateIPPrefix is the first octet of the deterministic private IPv4 address
// reported for an ActiveMQ instance.
const privateIPPrefix = 10

// computeInstances derives the stable per-instance console URL, wire-level
// endpoints and (ActiveMQ only) ENI IP address for a broker. The values are
// computed once at create from the broker id, engine type, deployment mode and
// region, then stored, so repeated DescribeBroker reads never drift.
func (m *Mock) computeInstances(brokerID, engineType, deploymentMode string) []driver.Instance {
	count := instanceCount(deploymentMode)
	out := make([]driver.Instance, 0, count)

	for i := 0; i < count; i++ {
		host := m.instanceHost(brokerID, i, count)
		out = append(out, driver.Instance{
			ConsoleURL: consoleURL(host, engineType),
			Endpoints:  endpoints(host, engineType),
			IPAddress:  m.instanceIP(brokerID, i, engineType),
		})
	}

	return out
}

// instanceCount returns the number of allocated instances for a deployment mode.
func instanceCount(deploymentMode string) int {
	switch deploymentMode {
	case deploymentActiveStandby:
		return activeStandbyInstanceCount
	case deploymentSingleInstance, deploymentClusterMultiAZ:
		return 1
	default:
		return 1
	}
}

// instanceHost returns the DNS host label for one instance. The first instance
// of a broker uses the bare broker id (per the console URL Amazon MQ reports);
// additional instances of a multi-AZ deployment are suffixed -N.
func (m *Mock) instanceHost(brokerID string, index, count int) string {
	if count == 1 {
		return fmt.Sprintf("%s.mq.%s.amazonaws.com", brokerID, m.opts.Region)
	}

	return fmt.Sprintf("%s-%d.mq.%s.amazonaws.com", brokerID, index+1, m.opts.Region)
}

// consoleURL returns the broker web console URL for an instance host.
func consoleURL(host, engineType string) string {
	if engineType == driver.EngineRabbitMQ {
		return "https://" + host
	}

	return fmt.Sprintf("https://%s:%d", host, portConsole)
}

// endpoints returns the ordered wire-level protocol endpoints for an instance
// host, keyed by engine type.
func endpoints(host, engineType string) []string {
	if engineType == driver.EngineRabbitMQ {
		return []string{fmt.Sprintf("amqps://%s:%d", host, portAMQP)}
	}

	return []string{
		fmt.Sprintf("ssl://%s:%d", host, portOpenWire),
		fmt.Sprintf("amqp+ssl://%s:%d", host, portAMQP),
		fmt.Sprintf("stomp+ssl://%s:%d", host, portSTOMP),
		fmt.Sprintf("mqtt+ssl://%s:%d", host, portMQTT),
		fmt.Sprintf("wss://%s:%d", host, portWSS),
	}
}

// instanceIP returns a stable, deterministic private IP address for an ActiveMQ
// instance. RabbitMQ brokers report no IP address, matching the real API.
func (m *Mock) instanceIP(brokerID string, index int, engineType string) string {
	if engineType == driver.EngineRabbitMQ {
		return ""
	}

	sum := sha256.Sum256([]byte(brokerID + ":" + m.opts.Region + ":" + fmt.Sprint(index)))
	ip := net.IPv4(privateIPPrefix, sum[0], sum[1], sum[2])

	return ip.String()
}
