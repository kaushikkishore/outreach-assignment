package main

import (
	"context"
	"time"
)

// ======================
// Configs Models
// ======================

// Centralized configuration for all CRM providers
type FieldMapping struct {
	InternalField string `json:"internal_field"`
	ExternalField string `json:"external_field"`
}

type ProviderConfig struct {
	ApiEndpoint  string         `json:"api_endpoint"`   // users/1
	FieldMapping []FieldMapping `json:"field_mapping"`  // Mapping for the field.
	RateLimitRPS int            `json:"rate_limit_rps"` // Rate limiting
	MaxWorkers   int            `json:"max_workers"`    // Max workers assigned.
}

type SystemConfig struct {
	Providers map[string]ProviderConfig `json:"providers"` // Key as name of the api endpoint who is service provider for the sync process.
}

// ======================
// Domain Models
// ======================

// Internal System A data structure
type InternalRecord struct {
	Id   string            `json:"id"`   // Internal system ID
	Data map[string]string `json:"data"` // Payload fields
}

// External System B data structure
type ExternalRecord struct {
	ExternalId string            `json:"external_id"` // Mapped from InternalRecord.Id
	Type       string            `json:"type"`        // From ProviderConfig.RecordType
	Fields     map[string]string `json:"fields"`      // Transformed data
}

// ======================
// Transformation Service
// ======================

type Transformer interface {
	Transform(rec *InternalRecord) (*ExternalRecord, error)
}

type SchemaTransformer struct {
	config *SystemConfig
}

// Transforms internal records to external CRM format using:
// - Field mapping rules from config
// - Operation type validation
// - Schema version handling
func NewSchemaTransformer(config *SystemConfig) Transformer {
	return &SchemaTransformer{config: config}
}

func (t *SchemaTransformer) Transform(rec *InternalRecord) (*ExternalRecord, error) {
	return nil, nil
}

// ======================
// Rate Limiter Service
// ======================

type RateLimiter interface {
	Wait(ctx context.Context, provider string) error
}

type DistributedRateLimiter struct {
	config *SystemConfig
}

// ======================
// EXTERNAL API Client
// ======================

type ExternalClient interface {
	SyncRecord(ctx context.Context, record *ExternalRecord) error
}

type CRMAPIClient struct {
	// Contains authentication, base URLs, etc.
}

// ======================
// Dead Letter Queue
// ======================

type DLQHandler interface {
	Push(record *InternalRecord, err error)
}

type KafkaDLQHandler struct {
	// Connection to Kafka dead letter topic
}

// ======================
// Metrics Service
// ======================

type MetricsRecorder interface {
	RecordSuccess()
	RecordFailure()
	RecordLatency(duration time.Duration)
}

type PrometheusRecorder struct {
	// Integrates with monitoring system
}

// ======================
// Worker Pool
// ======================

type WorkerPool struct {
	config         *SystemConfig
	transformer    Transformer
	rateLimiter    RateLimiter
	externalClient ExternalClient
	dlq            DLQHandler
	metrics        MetricsRecorder
}

// ======================
// Event Producer
// ======================

type EventProducer interface {
	Produce(ctx context.Context) <-chan *InternalRecord
}

type KafkaEventProducer struct {
	// Connection to source Kafka topic
}
