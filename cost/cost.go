// Package cost provides simulated cost tracking for cloud operations.
package cost

import (
	"sync"
)

// ServiceCost defines the cost structure for a single service operation.
type ServiceCost struct {
	Service   string
	Operation string
	UnitCost  float64
	Quantity  int
	Total     float64
}

// Tracker tracks simulated costs across all cloud operations.
type Tracker struct {
	mu    sync.RWMutex
	costs []ServiceCost
	rates map[string]float64
}

// New creates a new cost tracker with default cloud pricing rates.
func New() *Tracker {
	return &Tracker{
		costs: make([]ServiceCost, 0),
		rates: defaultRates(),
	}
}

// defaultRates returns approximate per-operation costs (simplified).
func defaultRates() map[string]float64 {
	return map[string]float64{
		// Compute (per instance-hour)
		"compute:RunInstances":       0.0116, // t2.micro equivalent
		"compute:StartInstances":     0.0,
		"compute:StopInstances":      0.0,
		"compute:TerminateInstances": 0.0,

		// Storage (per operation)
		"storage:PutObject":    0.000005, // $5 per 1M PUTs
		"storage:GetObject":    0.0000004,
		"storage:DeleteObject": 0.0,
		"storage:ListObjects":  0.000005,
		"storage:CreateBucket": 0.0,

		// Database (per operation)
		"database:PutItem":       0.00000125, // 1 WCU
		"database:GetItem":       0.00000025, // 0.5 RCU
		"database:Query":         0.00000025,
		"database:Scan":          0.00000025,
		"database:BatchPutItems": 0.00000125,
		"database:BatchGetItems": 0.00000025,

		// Serverless (per invocation)
		"serverless:Invoke": 0.0000002, // $0.20 per 1M

		// Message Queue (per operation)
		"messagequeue:SendMessage":     0.0000004, // $0.40 per 1M
		"messagequeue:ReceiveMessages": 0.0000004,

		// DNS (per query)
		"dns:CreateRecord": 0.0,
		"dns:GetRecord":    0.0000004,

		// Monitoring (per metric)
		"monitoring:PutMetricData": 0.00001, // $0.01 per 1K
		"monitoring:GetMetricData": 0.00001,

		// Networking
		"networking:CreateVPC":    0.0,
		"networking:CreateSubnet": 0.0,

		// Load Balancer (per hour)
		"loadbalancer:CreateLoadBalancer": 0.0225,

		// IAM (free)
		"iam:CreateUser":      0.0,
		"iam:CheckPermission": 0.0,

		// SageMaker (training/processing per instance-hour, hosting per
		// instance-hour, inference per request)
		"sagemaker:CreateTrainingJob":             0.115, // ml.m5.large-equivalent instance-hour
		"sagemaker:CreateProcessingJob":           0.115,
		"sagemaker:CreateTransformJob":            0.115,
		"sagemaker:CreateHyperParameterTuningJob": 0.115,
		"sagemaker:CreateEndpoint":                0.115,  // real-time hosting instance-hour
		"sagemaker:InvokeEndpoint":                0.0,    // bundled into hosting hours
		"sagemaker:CreateNotebookInstance":        0.0464, // ml.t3.medium-equivalent instance-hour
		"sagemaker:CreateModel":                   0.0,

		// Vertex AI (training/pipelines per node-hour, online prediction and
		// generateContent per request/call, registry resources free)
		"vertexai:CreateCustomJob":               0.19, // n1-standard-4-equivalent node-hour
		"vertexai:CreateHyperparameterTuningJob": 0.19,
		"vertexai:CreateTrainingPipeline":        0.19,
		"vertexai:CreatePipelineJob":             0.03, // pipeline run execution
		"vertexai:CreateBatchPredictionJob":      0.19,
		"vertexai:CreateTuningJob":               0.19,
		"vertexai:DeployModel":                   0.19,     // online prediction node-hour
		"vertexai:Predict":                       0.0,      // bundled into deployed node-hours
		"vertexai:GenerateContent":               0.000125, // per 1K input chars-equivalent call
		"vertexai:AssignNotebookRuntime":         0.15,     // managed notebook node-hour
		"vertexai:CreateModel":                   0.0,
		"vertexai:CreateEndpoint":                0.0,

		// Azure AI — Cognitive Services (AI Foundry / Azure OpenAI): inference
		// per call, deployments/accounts free (billed via consumed tokens).
		"azureai:ChatCompletions":  0.00001, // per-call proxy for token usage
		"azureai:Completions":      0.00001,
		"azureai:Embeddings":       0.000001,
		"azureai:CreateDeployment": 0.0,
		"azureai:CreateAccount":    0.0,

		// Azure AI — Machine Learning: compute/jobs/endpoints per node-hour,
		// assets and workspaces free.
		"azureai:CreateCompute":            0.19, // STANDARD_DS3_v2-equivalent node-hour
		"azureai:CreateJob":                0.19,
		"azureai:CreateEndpointDeployment": 0.19, // online-endpoint instance-hour
		"azureai:ScoreOnlineEndpoint":      0.0,  // bundled into instance-hours
		"azureai:CreateMLWorkspace":        0.0,
		"azureai:CreateEndpoint":           0.0,
	}
}

// Record records a cost event for a service operation.
func (t *Tracker) Record(service, operation string, quantity int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	key := service + ":" + operation

	rate, ok := t.rates[key]
	if !ok {
		rate = 0.0
	}

	total := rate * float64(quantity)

	t.costs = append(t.costs, ServiceCost{
		Service:   service,
		Operation: operation,
		UnitCost:  rate,
		Quantity:  quantity,
		Total:     total,
	})
}

// SetRate overrides the default rate for a service operation.
func (t *Tracker) SetRate(service, operation string, rate float64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.rates[service+":"+operation] = rate
}

// TotalCost returns the total simulated cost across all operations.
func (t *Tracker) TotalCost() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var total float64

	for _, c := range t.costs {
		total += c.Total
	}

	return total
}

// CostByService returns the total cost grouped by service.
func (t *Tracker) CostByService() map[string]float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make(map[string]float64)

	for _, c := range t.costs {
		result[c.Service] += c.Total
	}

	return result
}

// CostByOperation returns the total cost grouped by service:operation.
func (t *Tracker) CostByOperation() map[string]float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make(map[string]float64)

	for _, c := range t.costs {
		key := c.Service + ":" + c.Operation
		result[key] += c.Total
	}

	return result
}

// AllCosts returns a copy of all recorded cost events.
func (t *Tracker) AllCosts() []ServiceCost {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make([]ServiceCost, len(t.costs))
	copy(result, t.costs)

	return result
}

// Reset clears all recorded costs.
func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.costs = t.costs[:0]
}
