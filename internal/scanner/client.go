package scanner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrUnavailable reports that the scanner could not be reached, or that it
// is busy and asks to be called again later.
var ErrUnavailable = errors.New("scanner unavailable")

// UnavailableError is an ErrUnavailable that says when to try again.
type UnavailableError struct {
	// RetryAfter is how long the scanner asked to wait, 0 when it did not.
	RetryAfter time.Duration
	Err        error
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("%s: %v", ErrUnavailable, e.Err)
}

// Is makes errors.Is(err, ErrUnavailable) hold.
func (e *UnavailableError) Is(target error) bool { return target == ErrUnavailable }

func (e *UnavailableError) Unwrap() error { return e.Err }

// Client calls the scanner.
type Client struct {
	// URL is the base URL of the scanner, such as
	// http://kokumi-scanner.kokumi.svc:8080.
	URL string
	// HTTP sends the requests. A scan takes as long as its context allows.
	HTTP *http.Client
}

// NewClient returns a Client for the scanner at url.
func NewClient(url string) *Client {
	return &Client{URL: strings.TrimSuffix(url, "/"), HTTP: &http.Client{}}
}

// Scan scans the images of req and returns the results. It fails with an
// error wrapping ErrUnavailable when the scanner cannot be reached or is
// busy.
func (c *Client) Scan(ctx context.Context, req Request) (*Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encoding scan request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+ScanPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating scan request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		return nil, &UnavailableError{Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	// A response holds every image's SBOM and report.
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(len(req.Images)+1)*2*MaxOutputSize))
	if err != nil {
		return nil, fmt.Errorf("reading scan response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusServiceUnavailable:
		retryAfter, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return nil, &UnavailableError{RetryAfter: time.Duration(retryAfter) * time.Second, Err: errors.New(errorMessage(data))}
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("scanner answered %s: %s", resp.Status, errorMessage(data))
	}

	var out Response
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decoding scan response: %w", err)
	}
	if len(out.Images) != len(req.Images) {
		return nil, fmt.Errorf("scanner returned %d results for %d images", len(out.Images), len(req.Images))
	}
	return &out, nil
}

// errorMessage returns the message of an error response body.
func errorMessage(data []byte) string {
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &body) == nil && body.Error != "" {
		return body.Error
	}
	return strings.TrimSpace(string(data))
}
