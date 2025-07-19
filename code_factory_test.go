package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Extend MockAPIClient for testing: track called records.
type TestMockAPIClient struct {
	MockAPIClient
	CalledRecords []ExternalRecord // Records passed to CreateRecord
	CallErrors    []error         // Sequence of errors to return (for retry testing)
	callIndex     int
}

func (c *TestMockAPIClient) CreateRecord(rec ExternalRecord) error {
	c.CalledRecords = append(c.CalledRecords, rec)
	if len(c.CallErrors) > c.callIndex {
		err := c.CallErrors[c.callIndex]
		c.callIndex++
		return err
	}
	return nil
}

func TestNewSyncerFactory(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		wantErr  bool
	}{
		{"Valid CRM1", "crm1", false},
		{"Valid CRM2", "crm2", false},
		{"Invalid Provider", "unknown", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given: Dummy limiter and client
			limiter := NewTokenBucketLimiter(1, time.Second)
			client := &MockAPIClient{}

			// When: Create syncer via factory
			_, err := NewSyncer(tt.provider, limiter, client)

			// Then: Check error
			if (err != nil) != tt.wantErr {
				t.Errorf("NewSyncer() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCRM1SyncerSync(t *testing.T) {
	tests := []struct {
		name           string
		rec            InternalRecord
		limiterTokens  int
		apiCallErrors  []error // For retry simulation
		wantErr        bool
		wantCalledRec  ExternalRecord
		wantDLQ        bool // Expect permanent failure
	}{
		{
			name:          "Success",
			rec:           InternalRecord{ID: "1", Name: "John Doe", Email: "john@example.com"},
			limiterTokens: 1,
			apiCallErrors: nil,
			wantErr:       false,
			wantCalledRec: ExternalRecord{Id: "1", FullName: "John Doe", ContactEmail: "john@example.com"},
			wantDLQ:       false,
		},
		{
			name:          "Validation Failure",
			rec:           InternalRecord{ID: "", Name: "John Doe", Email: "john@example.com"},
			limiterTokens: 1,
			apiCallErrors: nil,
			wantErr:       true,
			wantCalledRec: ExternalRecord{}, // No call
			wantDLQ:       false,
		},
		{
			name:          "Rate Limit Exceeded With Retry Success",
			rec:           InternalRecord{ID: "1", Name: "John Doe", Email: "john@example.com"},
			limiterTokens: 0, // Initial no tokens
			apiCallErrors: nil,
			wantErr:       false,
			wantCalledRec: ExternalRecord{Id: "1", FullName: "John Doe", ContactEmail: "john@example.com"},
			wantDLQ:       false,
		},
		{
			name:           "Transient Failure Retry Success",
			rec:            InternalRecord{ID: "1", Name: "John Doe", Email: "john@example.com"},
			limiterTokens:  1,
			apiCallErrors:  []error{errors.New("transient"), nil}, // Fail first, succeed second
			wantErr:        false,
			wantCalledRec:  ExternalRecord{Id: "1", FullName: "John Doe", ContactEmail: "john@example.com"},
			wantDLQ:        false,
		},
		{
			name:           "Permanent Failure To DLQ",
			rec:            InternalRecord{ID: "1", Name: "John Doe", Email: "john@example.com"},
			limiterTokens:  1,
			apiCallErrors:  []error{errors.New("permanent"), errors.New("permanent"), errors.New("permanent"), errors.New("permanent")},
			wantErr:        true,
			wantCalledRec:  ExternalRecord{Id: "1", FullName: "John Doe", ContactEmail: "john@example.com"},
			wantDLQ:        true, // Expect error after 3 retries
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given: Setup limiter (with initial tokens), client with error sequence
			limiter := NewTokenBucketLimiter(tt.limiterTokens, time.Millisecond) // Fast refill for test
			client := &TestMockAPIClient{CallErrors: tt.apiCallErrors}
			syncer := &CRM1Syncer{rateLimiter: limiter, apiClient: client}

			// When: Perform sync (with small sleep for refill if needed)
			if tt.limiterTokens == 0 {
				time.Sleep(2 * time.Millisecond) // Allow refill
			}
			err := syncer.Sync(context.Background(), tt.rec)

			// Then: Assert error, called record, and DLQ implication
			if (err != nil) != tt.wantErr {
				t.Errorf("Sync() error = %v, wantErr %v", err, tt.wantErr)
			}
			if len(client.CalledRecords) > 0 {
				if client.CalledRecords[len(client.CalledRecords)-1] != tt.wantCalledRec {
					t.Errorf("Called record = %+v, want %+v", client.CalledRecords[0], tt.wantCalledRec)
				}
			} else if tt.wantCalledRec != (ExternalRecord{}) {
				t.Error("Expected API call but none made")
			}
			if tt.wantDLQ && err == nil {
				t.Error("Expected DLQ (error after retries) but got success")
			}
		})
	}
}

func TestCRM2SyncerSync(t *testing.T) {
	// Similar to CRM1, but test name split transformation
	t.Run("Success With Name Split", func(t *testing.T) {
		// Given: Valid record, limiter, client
		rec := InternalRecord{ID: "1", Name: "John Doe", Email: "john@example.com"}
		limiter := NewTokenBucketLimiter(1, time.Second)
		client := &TestMockAPIClient{}
		syncer := &CRM2Syncer{rateLimiter: limiter, apiClient: client}

		// When: Sync
		err := syncer.Sync(context.Background(), rec)

		// Then: No error, correct transformed record (name split)
		if err != nil {
			t.Errorf("Sync() error = %v, want nil", err)
		}
		wantRec := ExternalRecord{Id: "1", FirstName: "John ", LastName: "Doe"} // Based on naive split
		if len(client.CalledRecords) != 1 || client.CalledRecords[0] != wantRec {
			t.Errorf("Called record = %+v, want %+v", client.CalledRecords[0], wantRec)
		}
	})
}

func TestTokenBucketLimiter(t *testing.T) {
	t.Run("Acquire And Refill", func(t *testing.T) {
		// Given: Limiter with 1 token, fast refill
		limiter := NewTokenBucketLimiter(1, time.Millisecond)

		// When: Acquire once (success), then immediately (fail), wait and acquire again (success)
		if err := limiter.Acquire(); err != nil {
			t.Errorf("First acquire error = %v, want nil", err)
		}
		if err := limiter.Acquire(); err == nil {
			t.Error("Second acquire = nil, want error")
		}
		time.Sleep(2 * time.Millisecond) // Allow refill
		if err := limiter.Acquire(); err != nil {
			t.Errorf("Third acquire error = %v, want nil", err)
		}
	})
}
