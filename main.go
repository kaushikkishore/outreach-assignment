package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// InternalRecord represents a record from System A (internal).
type InternalRecord struct {
	ID    string
	Name  string
	Email string
}

// ExternalRecord is a generic external representation (providers may vary).
type ExternalRecord struct {
	Id           string
	FullName     string // crm1 uses this
	ContactEmail string // crm1 uses this
	FirstName    string // crm2 uses this (split name)
	LastName     string // crm2 uses this
}

// Syncer defines the interface for syncing records to a provider.
type Syncer interface {
	Sync(ctx context.Context, rec InternalRecord) error
	SyncOutbound(ctx context.Context, rec ExternalRecord) error
	IsSyncedAllRound(ctx context.Context, id string) (bool, error)
}

// RateLimiter defines an interface for rate limiting.
type RateLimiter interface {
	Acquire() error
}

// APIClient defines an interface for external API calls.
type APIClient interface {
	CreateRecord(rec ExternalRecord) error
	CreateOutboundRecord(rec InternalRecord) error

	// Should be part of different interface.
	// Which will go ahead and check in different services.
	// This job is also to do the due deligence that all data fields are synced.
	// We can see what sync means in both places.
	IsSynced(id string) (bool, error)
	IsSyncedInCRMSide(id string) (bool, error)
}

// TokenBucketLimiter is a simple in-memory token bucket rate limiter.
type TokenBucketLimiter struct {
	mu         sync.Mutex
	tokens     int
	maxTokens  int
	refillRate time.Duration
	lastRefill time.Time
}

func NewTokenBucketLimiter(maxTokens int, refillRate time.Duration) *TokenBucketLimiter {
	return &TokenBucketLimiter{
		tokens:     maxTokens,
		maxTokens:  maxTokens,
		refillRate: refillRate,
		lastRefill: time.Now(),
	}
}

func (l *TokenBucketLimiter) Acquire() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(l.lastRefill)
	tokensToAdd := int(elapsed / l.refillRate)
	if tokensToAdd > 0 {
		l.tokens = min(l.tokens+tokensToAdd, l.maxTokens)
		l.lastRefill = now
	}

	if l.tokens > 0 {
		l.tokens--
		return nil
	}
	return errors.New("rate limit exceeded")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// MockAPIClient simulates external API calls (with configurable failure).
type MockAPIClient struct {
	ShouldFail bool // For testing failures
}

func (c *MockAPIClient) CreateRecord(rec ExternalRecord) error {
	if c.ShouldFail {
		return errors.New("simulated API failure")
	}
	fmt.Printf("Mock API Create: %+v\n", rec)
	return nil
}

func (c *MockAPIClient) CreateOutboundRecord(rec InternalRecord) error {
	if c.ShouldFail {
		return errors.New("simulated API failure")
	}
	fmt.Printf("Mock API Create Outbound Record: %+v\n", rec)
	return nil
}

func (c *MockAPIClient) IsSynced(id string) (bool, error) {
	return true, nil
}

func (c *MockAPIClient) IsSyncedInCRMSide(id string) (bool, error) {
	return true, nil
}

// NewSyncer is the Factory function to create provider-specific Syncers.
func NewSyncer(provider string, rateLimiter RateLimiter, apiClient APIClient) (Syncer, error) {
	switch provider {
	case "crm1":
		return &CRM1Syncer{rateLimiter: rateLimiter, apiClient: apiClient}, nil
	case "crm2":
		return &CRM2Syncer{rateLimiter: rateLimiter, apiClient: apiClient}, nil
	default:
		return nil, errors.New("unknown provider")
	}
}

// CRM1Syncer implements Syncer for "crm1" provider (e.g., Salesforce-like).
type CRM1Syncer struct {
	rateLimiter RateLimiter
	apiClient   APIClient
}

func (s *CRM1Syncer) Sync(ctx context.Context, rec InternalRecord) error {
	// Basic validation
	if rec.ID == "" || rec.Name == "" || rec.Email == "" {
		return errors.New("invalid record: missing fields")
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ { // 3 retries
		if err := s.rateLimiter.Acquire(); err != nil {
			lastErr = err
			time.Sleep(time.Duration(math.Pow(2, float64(attempt))) * time.Second) // Exp backoff: 1s, 2s, 4s
			continue
		}

		// Transform schema for crm1
		extRec := ExternalRecord{
			Id:           rec.ID,
			FullName:     rec.Name,
			ContactEmail: rec.Email,
		}

		err := s.apiClient.CreateRecord(extRec)
		if err == nil {
			return nil // Success
		}
		lastErr = err
		// Assume transient if not permanent; retry
		time.Sleep(time.Duration(math.Pow(2, float64(attempt))) * time.Second)
	}

	// Permanent failure: Send to DLQ (simulated by logging)
	fmt.Printf("DLQ: Failed record after retries: %+v, Error: %v\n", rec, lastErr)
	return lastErr
}

func (s *CRM1Syncer) SyncOutbound(ctx context.Context, rec ExternalRecord) error {
	// Basic validation

	if rec.Id == "" {
		return errors.New("invalid internal record: missing fields")
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ { // 3 retries
		if err := s.rateLimiter.Acquire(); err != nil {
			lastErr = err
			time.Sleep(time.Duration(math.Pow(2, float64(attempt))) * time.Second) // Exp backoff: 1s, 2s, 4s
			continue
		}

		// Transform schema for crm1
		extRec := InternalRecord{
			ID:    rec.Id,
			Name:  rec.FullName,
			Email: rec.ContactEmail,
		}

		err := s.apiClient.CreateOutboundRecord(extRec)
		if err == nil {
			return nil // Success
		}
		lastErr = err
		// Assume transient if not permanent; retry
		time.Sleep(time.Duration(math.Pow(2, float64(attempt))) * time.Second)
	}

	// Permanent failure: Send to DLQ (simulated by logging)
	fmt.Printf("[Outbound] DLQ: Failed record after retries: %+v, Error: %v\n", rec, lastErr)
	return lastErr
}

func (s *CRM1Syncer) IsSyncedAllRound(ctx context.Context, id string) (bool, error) {
	// Check in out db
	isSynced, err := s.apiClient.IsSynced(id)
	if err != nil {
		fmt.Print("Record synced at our database failed.")
		return false, err
	}
	fmt.Print("Reord Synced successfully %s ", isSynced)

	// Check in crm db
	isSynced, err = s.apiClient.IsSyncedInCRMSide(id)
	if err != nil {
		fmt.Print("Record synced at CRM Side failed.")
		return false, err
	}
	fmt.Printf("Reord Synced properly at crm side. successfully %s", isSynced)

	return true, nil
}

// CRM2Syncer implements Syncer for "crm2" provider (e.g., Hubspot-like, with name split).
type CRM2Syncer struct {
	rateLimiter RateLimiter
	apiClient   APIClient
}

func (s *CRM2Syncer) Sync(ctx context.Context, rec InternalRecord) error {
	// Basic validation
	if rec.ID == "" || rec.Name == "" || rec.Email == "" {
		return errors.New("invalid record: missing fields")
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := s.rateLimiter.Acquire(); err != nil {
			lastErr = err
			time.Sleep(time.Duration(math.Pow(2, float64(attempt))) * time.Second)
			continue
		}

		// Transform schema for crm2 (split name into first/last)
		firstName, lastName := splitName(rec.Name) // Helper function
		// Transform the epoch to ISO Format

		extRec := ExternalRecord{
			Id:        rec.ID,
			FirstName: firstName,
			LastName:  lastName,
		}

		err := s.apiClient.CreateRecord(extRec)
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(time.Duration(math.Pow(2, float64(attempt))) * time.Second)
	}

	// DLQ simulation
	fmt.Printf("DLQ: Failed record after retries: %+v, Error: %v\n", rec, lastErr)
	return lastErr
}

func (s *CRM2Syncer) SyncOutbound(ctx context.Context, rec ExternalRecord) error {
	// Basic validation

	if rec.Id == "" {
		return errors.New("invalid internal record: missing fields")
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ { // 3 retries
		if err := s.rateLimiter.Acquire(); err != nil {
			lastErr = err
			time.Sleep(time.Duration(math.Pow(2, float64(attempt))) * time.Second) // Exp backoff: 1s, 2s, 4s
			continue
		}

		// Transform schema for crm1
		extRec := InternalRecord{
			ID:    rec.Id,
			Name:  rec.FullName,
			Email: rec.ContactEmail,
		}

		err := s.apiClient.CreateOutboundRecord(extRec)
		if err == nil {
			return nil // Success
		}
		lastErr = err
		// Assume transient if not permanent; retry
		time.Sleep(time.Duration(math.Pow(2, float64(attempt))) * time.Second)
	}

	// Permanent failure: Send to DLQ (simulated by logging)
	fmt.Printf("[Outbound] DLQ: Failed record after retries: %+v, Error: %v\n", rec, lastErr)
	return lastErr
}

func (s *CRM2Syncer) IsSyncedAllRound(ctx context.Context, id string) (bool, error) {
	// Check in out db
	isSynced, err := s.apiClient.IsSynced(id)
	if err != nil {
		fmt.Print("Record synced at our database failed.")
		return false, err
	}
	fmt.Print("Reord Synced successfully %s ", isSynced)

	// Check in crm db
	isSynced, err = s.apiClient.IsSyncedInCRMSide(id)
	if err != nil {
		fmt.Print("Record synced at CRM Side failed.")
		return false, err
	}
	fmt.Printf("Reord Synced properly at crm side. successfully %s", isSynced)

	return true, nil
}

// Helper: Split name into first/last (improved to handle spaces properly).
func splitName(fullName string) (string, string) {
	parts := strings.SplitN(fullName, " ", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return fullName, ""
}

func main() {
	// Config: Adjust based on provider API limits (e.g., 10 RPS)
	limiter := NewTokenBucketLimiter(10, time.Second) // 10 tokens/sec
	apiClient := &MockAPIClient{ShouldFail: false}    // Set to true for failure testing

	// Use Factory to create syncer
	syncer, err := NewSyncer("crm1", limiter, apiClient)
	if err != nil {
		panic(err)
	}

	// Simulate worker pool: 20 concurrent syncs
	var wg sync.WaitGroup
	for i := 0; i < 1; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rec := InternalRecord{
				ID:    fmt.Sprintf("%d", id),
				Name:  "John Doe",
				Email: "john@example.com",
			}
			err := syncer.Sync(context.Background(), rec)
			if err != nil {
				fmt.Printf("Sync failed for %d: %v\n", id, err)
			}
			// Sync outbound
			externalRecord := ExternalRecord{
				Id:           fmt.Sprintf("%d", id),
				FullName:     "John Doe Outbound",
				ContactEmail: "john@outbound.in",
				FirstName:    "John",
				LastName:     "Doe",
			}
			err = syncer.SyncOutbound(context.Background(), externalRecord)
			if err != nil {
				fmt.Printf("Sync failed for %d: %v\n", id, err)
			}

			isSynced, err := syncer.IsSyncedAllRound(context.Background(), fmt.Sprintf("%d", id))
			if err != nil {
				fmt.Printf("Both system sync check failed for %d: %v\n", id, err)
			}

			fmt.Printf("System sync status %v", isSynced)
		}(i)
	}
	wg.Wait()
	fmt.Println("All syncs completed.")
}

/*
01. Create a single record [Available at somewhere database] - which we wanted to sync to out CRM and internal Sync that all fields are correct

*/
