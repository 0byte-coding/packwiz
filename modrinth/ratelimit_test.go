package modrinth

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRateLimitRetry verifies that the rate limit handler retries on 429 responses
func TestRateLimitRetry(t *testing.T) {
	var attemptCount atomic.Int32

	// Create a test server that returns 429 for the first 2 attempts, then succeeds
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attemptCount.Add(1)
		if attempt <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate_limit","description":"You are being rate-limited. Please wait 100 milliseconds. 0/300 remaining."}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
	}))
	defer server.Close()

	// Create a client with our rate limit transport
	client := &http.Client{
		Transport: &rateLimitTransport{
			Transport:  http.DefaultTransport,
			MaxRetries: 5,
		},
	}

	// Make a request
	start := time.Now()
	resp, err := client.Get(server.URL)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status OK, got %d", resp.StatusCode)
	}

	if attemptCount.Load() != 3 {
		t.Errorf("Expected 3 attempts (2 rate limits + 1 success), got %d", attemptCount.Load())
	}

	// Should have waited at least 200ms total (2 attempts * ~100ms each, with buffer)
	if duration < 200*time.Millisecond {
		t.Errorf("Expected at least 200ms duration for retries, got %v", duration)
	}

	t.Logf("Test completed successfully with %d attempts in %v", attemptCount.Load(), duration)
}

// TestRateLimitMaxRetriesExceeded verifies that the handler gives up after max retries
func TestRateLimitMaxRetriesExceeded(t *testing.T) {
	var attemptCount atomic.Int32

	// Create a test server that always returns 429
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate_limit","description":"You are being rate-limited. Please wait 50 milliseconds. 0/300 remaining."}`))
	}))
	defer server.Close()

	// Create a client with only 3 max retries
	client := &http.Client{
		Transport: &rateLimitTransport{
			Transport:  http.DefaultTransport,
			MaxRetries: 3,
		},
	}

	// Make a request
	resp, err := client.Get(server.URL)

	// Should get an error after max retries
	if err == nil {
		resp.Body.Close()
		t.Fatal("Expected error after max retries, got nil")
	}

	if !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Errorf("Expected 'rate limit exceeded' error, got: %v", err)
	}

	// Should have attempted MaxRetries + 1 times (initial + retries)
	if attemptCount.Load() != 4 {
		t.Errorf("Expected 4 attempts (1 initial + 3 retries), got %d", attemptCount.Load())
	}

	t.Logf("Test completed successfully, failed after %d attempts as expected", attemptCount.Load())
}

// TestRateLimitParseWaitTime verifies wait time extraction from different formats
func TestRateLimitParseWaitTime(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected time.Duration
	}{
		{
			name:     "Milliseconds format",
			body:     `{"error":"rate_limit","description":"You are being rate-limited. Please wait 500 milliseconds. 0/300 remaining."}`,
			expected: 500 * time.Millisecond,
		},
		{
			name:     "Seconds format",
			body:     `{"error":"rate_limit","description":"You are being rate-limited. Please wait 2 seconds. 0/300 remaining."}`,
			expected: 2 * time.Second,
		},
		{
			name:     "No match returns 0",
			body:     `{"error":"rate_limit","description":"Rate limited"}`,
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractWaitTime(tt.body)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestRateLimitRetryAfterHeader verifies that Retry-After header is respected
func TestRateLimitRetryAfterHeader(t *testing.T) {
	var attemptCount atomic.Int32

	// Create a test server that returns 429 with Retry-After header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attemptCount.Add(1)
		if attempt == 1 {
			w.Header().Set("Retry-After", "0.1") // 100ms
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate_limit"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
	}))
	defer server.Close()

	client := &http.Client{
		Transport: &rateLimitTransport{
			Transport:  http.DefaultTransport,
			MaxRetries: 5,
		},
	}

	start := time.Now()
	resp, err := client.Get(server.URL)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status OK, got %d", resp.StatusCode)
	}

	// Should have waited at least 100ms (from Retry-After header, plus buffer)
	if duration < 100*time.Millisecond {
		t.Errorf("Expected at least 100ms duration, got %v", duration)
	}

	t.Logf("Test completed successfully, waited %v as specified by Retry-After header", duration)
}

// TestRateLimitExponentialBackoff verifies exponential backoff when no wait time is specified
func TestRateLimitExponentialBackoff(t *testing.T) {
	var attemptCount atomic.Int32

	// Create a test server that returns 429 without wait time info
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attemptCount.Add(1)
		if attempt <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate_limit"}`)) // No wait time specified
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
	}))
	defer server.Close()

	client := &http.Client{
		Transport: &rateLimitTransport{
			Transport:  http.DefaultTransport,
			MaxRetries: 5,
		},
	}

	start := time.Now()
	resp, err := client.Get(server.URL)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status OK, got %d", resp.StatusCode)
	}

	// First backoff: 100ms * 2^0 = 100ms + buffer (~110-150ms)
	// Second backoff: 100ms * 2^1 = 200ms + buffer (~220-250ms)
	// Total should be at least 300ms
	if duration < 300*time.Millisecond {
		t.Errorf("Expected at least 300ms for exponential backoff, got %v", duration)
	}

	t.Logf("Test completed successfully with exponential backoff, total duration: %v", duration)
}

// TestRateLimitNonRateLimitError verifies that non-429 errors are returned immediately
func TestRateLimitNonRateLimitError(t *testing.T) {
	var attemptCount atomic.Int32

	// Create a test server that returns 500 (server error)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal_server_error"}`))
	}))
	defer server.Close()

	client := &http.Client{
		Transport: &rateLimitTransport{
			Transport:  http.DefaultTransport,
			MaxRetries: 5,
		},
	}

	resp, err := client.Get(server.URL)

	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", resp.StatusCode)
	}

	// Should only attempt once (no retries for non-429)
	if attemptCount.Load() != 1 {
		t.Errorf("Expected 1 attempt (no retries for non-429), got %d", attemptCount.Load())
	}

	t.Log("Test completed successfully, non-429 error returned immediately without retries")
}

// TestRateLimitHighRetryCount simulates the actual production scenario with 50 max retries
func TestRateLimitHighRetryCount(t *testing.T) {
	var attemptCount atomic.Int32

	// Create a test server that rate limits for 10 attempts, then succeeds
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attemptCount.Add(1)
		if attempt <= 10 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate_limit","description":"Please wait 10 milliseconds"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
	}))
	defer server.Close()

	client := &http.Client{
		Transport: &rateLimitTransport{
			Transport:  http.DefaultTransport,
			MaxRetries: 50, // Production value
		},
	}

	start := time.Now()
	resp, err := client.Get(server.URL)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status OK, got %d", resp.StatusCode)
	}

	if attemptCount.Load() != 11 {
		t.Errorf("Expected 11 attempts (10 rate limits + 1 success), got %d", attemptCount.Load())
	}

	t.Logf("Test completed successfully with high retry count: %d attempts in %v", attemptCount.Load(), duration)
}

// BenchmarkRateLimitOverhead measures the overhead of the rate limit transport for successful requests
func BenchmarkRateLimitOverhead(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
	}))
	defer server.Close()

	client := &http.Client{
		Transport: &rateLimitTransport{
			Transport:  http.DefaultTransport,
			MaxRetries: 50,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Get(server.URL)
		if err != nil {
			b.Fatalf("Request failed: %v", err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
	}
}

// TestRateLimitConcurrentRequests verifies rate limit handling with concurrent requests
func TestRateLimitConcurrentRequests(t *testing.T) {
	var attemptCount atomic.Int32
	var rateLimitCount atomic.Int32

	// Create a test server that rate limits the first 5 requests
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount.Add(1)
		rlCount := rateLimitCount.Load()
		if rlCount < 5 {
			rateLimitCount.Add(1)
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate_limit","description":"Please wait 50 milliseconds"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
	}))
	defer server.Close()

	client := &http.Client{
		Transport: &rateLimitTransport{
			Transport:  http.DefaultTransport,
			MaxRetries: 50,
		},
	}

	// Make 3 concurrent requests
	errChan := make(chan error, 3)
	for i := 0; i < 3; i++ {
		go func(id int) {
			resp, err := client.Get(server.URL)
			if err != nil {
				errChan <- fmt.Errorf("request %d failed: %w", id, err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errChan <- fmt.Errorf("request %d got status %d", id, resp.StatusCode)
				return
			}
			errChan <- nil
		}(i)
	}

	// Wait for all requests to complete
	for i := 0; i < 3; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent request failed: %v", err)
		}
	}

	t.Logf("Concurrent requests completed successfully: %d total attempts, %d rate limited",
		attemptCount.Load(), rateLimitCount.Load())
}
