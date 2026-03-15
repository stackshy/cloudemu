// Package driver defines the interface for serverless function service implementations.
package driver

import "context"

// FunctionConfig describes a serverless function to create.
type FunctionConfig struct {
	Name        string
	Runtime     string
	Handler     string
	Memory      int // MB
	Timeout     int // seconds
	Environment map[string]string
	Tags        map[string]string
}

// FunctionInfo describes a serverless function.
type FunctionInfo struct {
	Name         string
	ARN          string
	Runtime      string
	Handler      string
	Memory       int
	Timeout      int
	State        string
	Environment  map[string]string
	Tags         map[string]string
	LastModified string
}

// InvokeInput configures a function invocation.
type InvokeInput struct {
	FunctionName string
	Payload      []byte
	InvokeType   string // "RequestResponse" or "Event"
}

// InvokeOutput is the result of a function invocation.
type InvokeOutput struct {
	StatusCode int
	Payload    []byte
	Error      string
}

// HandlerFunc is a function handler that processes invocations.
type HandlerFunc func(ctx context.Context, payload []byte) ([]byte, error)

// Serverless is the interface that serverless provider implementations must satisfy.
type Serverless interface {
	CreateFunction(ctx context.Context, config FunctionConfig) (*FunctionInfo, error)
	DeleteFunction(ctx context.Context, name string) error
	GetFunction(ctx context.Context, name string) (*FunctionInfo, error)
	ListFunctions(ctx context.Context) ([]FunctionInfo, error)
	UpdateFunction(ctx context.Context, name string, config FunctionConfig) (*FunctionInfo, error)
	Invoke(ctx context.Context, input InvokeInput) (*InvokeOutput, error)
	RegisterHandler(name string, handler HandlerFunc)
}
