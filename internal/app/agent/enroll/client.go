package enroll

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	requestTimeout = 30 * time.Second
	maxResponse    = 1 << 20
)

// Errors the loop reacts to.
var (
	errNoCodes   = errors.New("no codes left")
	errTransient = errors.New("transient error")
)

type codeResult struct {
	EnrollmentID string    `json:"enrollment_id"`
	PollSecret   string    `json:"poll_secret"`
	Code         string    `json:"code"`
	ExpiresAt    time.Time `json:"expires_at"`
	CodesLeft    int       `json:"codes_left"`
}

type statusResult struct {
	State         string     `json:"state"`
	Code          string     `json:"code"`
	ExpiresAt     *time.Time `json:"expires_at"`
	CodesLeft     int        `json:"codes_left"`
	ServerID      string     `json:"server_id"`
	Certificate   string     `json:"certificate"`
	CACertificate string     `json:"ca_certificate"`
	HubAddr       string     `json:"hub_addr"`
	// HubFingerprints are the hub certificates the API trusts (lower-case
	// hex SHA-256 of the DER); empty when it trusts none yet.
	HubFingerprints []string `json:"hub_fingerprints"`
}

type envelope struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type client struct {
	base string
	http *http.Client
}

func newClient(base string) *client {
	return &client{base: base, http: &http.Client{
		Timeout: requestTimeout,
		// The API never redirects; following one could resend the CSR or
		// poll secret over plain http or to another host.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// post sends a JSON body and decodes the API envelope's data into out.
// 429 and 5xx (and network errors) wrap errTransient; 409 is errNoCodes.
func (c *client) post(ctx context.Context, path string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", errTransient, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return fmt.Errorf("%w: %v", errTransient, err)
	}
	var env envelope
	_ = json.Unmarshal(b, &env)
	switch {
	case resp.StatusCode == http.StatusConflict:
		return errNoCodes
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return fmt.Errorf("%w: HTTP %d %s", errTransient, resp.StatusCode, env.Message)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("the server refused the request (HTTP %d): %s", resp.StatusCode, env.Message)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("unexpected response: %w", err)
	}
	return nil
}

func (c *client) start(ctx context.Context, csr, hostname string) (codeResult, error) {
	var r codeResult
	err := c.post(ctx, "/api/v1/agent/enroll", map[string]string{"csr": csr, "hostname": hostname}, &r)
	return r, err
}

func (c *client) refresh(ctx context.Context, secret string) (codeResult, error) {
	var r codeResult
	err := c.post(ctx, "/api/v1/agent/enroll/refresh", map[string]string{"poll_secret": secret}, &r)
	return r, err
}

func (c *client) status(ctx context.Context, secret string) (statusResult, error) {
	var r statusResult
	err := c.post(ctx, "/api/v1/agent/enroll/status", map[string]string{"poll_secret": secret}, &r)
	return r, err
}
