package modrinth

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

// rateLimitTransport wraps an http.RoundTripper and adds retry logic for rate limit errors
type rateLimitTransport struct {
	Transport http.RoundTripper
	MaxRetries int
}

// RoundTrip implements the http.RoundTripper interface with rate limit retry logic
func (t *rateLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.Transport == nil {
		t.Transport = http.DefaultTransport
	}
	if t.MaxRetries == 0 {
		t.MaxRetries = 5
	}

	var resp *http.Response
	var err error

	for attempt := 0; attempt <= t.MaxRetries; attempt++ {
		// Clone the request for retries (required because the body can only be read once)
		reqClone := req.Clone(req.Context())

		resp, err = t.Transport.RoundTrip(reqClone)
		if err != nil {
			return resp, err
		}

		// If we got a 429 (Too Many Requests), handle retry
		if resp.StatusCode == http.StatusTooManyRequests {
			// Read the response body to extract wait time
			bodyBytes, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()

			if readErr != nil {
				return resp, fmt.Errorf("failed to read rate limit response: %w", readErr)
			}

			bodyStr := string(bodyBytes)

			// Try to extract wait time from error message
			// Example: "You are being rate-limited. Please wait 20 milliseconds. 0/300 remaining."
			waitTime := extractWaitTime(bodyStr)

			// If we couldn't parse it, try Retry-After header
			if waitTime == 0 {
				if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
					if seconds, parseErr := strconv.ParseFloat(retryAfter, 64); parseErr == nil {
						waitTime = time.Duration(seconds * float64(time.Second))
					}
				}
			}

			// Default to exponential backoff if we couldn't determine wait time
			if waitTime == 0 {
				waitTime = time.Duration(100*(1<<uint(attempt))) * time.Millisecond
			}

			// Add a small buffer to the wait time (10% + 50ms)
			waitTime = waitTime + (waitTime / 10) + (50 * time.Millisecond)

			if attempt < t.MaxRetries {
				fmt.Printf("Rate limited by Modrinth API, waiting %v before retry (attempt %d/%d)...\n",
					waitTime, attempt+1, t.MaxRetries)
				time.Sleep(waitTime)
				continue
			}

			// Max retries exceeded, return the error response
			return resp, fmt.Errorf("rate limit exceeded after %d retries - Modrinth API is heavily rate limiting requests. Please try again later or contact Modrinth support if this persists", t.MaxRetries)
		}

		// Success or non-rate-limit error
		return resp, nil
	}

	return resp, err
}

// extractWaitTime attempts to extract the wait time from Modrinth's rate limit error message
func extractWaitTime(body string) time.Duration {
	// Pattern: "Please wait X milliseconds" or "Please wait X seconds"
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`Please wait (\d+) milliseconds?`),
		regexp.MustCompile(`Please wait (\d+) seconds?`),
	}

	for i, pattern := range patterns {
		matches := pattern.FindStringSubmatch(body)
		if len(matches) >= 2 {
			if value, err := strconv.ParseInt(matches[1], 10, 64); err == nil {
				if i == 0 {
					// Milliseconds
					return time.Duration(value) * time.Millisecond
				} else {
					// Seconds
					return time.Duration(value) * time.Second
				}
			}
		}
	}

	return 0
}

// newRateLimitHTTPClient creates a new HTTP client with rate limit retry logic
func newRateLimitHTTPClient() *http.Client {
	return &http.Client{
		Transport: &rateLimitTransport{
			Transport:  http.DefaultTransport,
			MaxRetries: 100, // 100 might be a bit high, 50 should be a good upper limit
		},
	}
}
