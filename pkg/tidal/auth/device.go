package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DeviceAuth is the result of starting the device authorization grant. The
// caller shows VerificationURIComplete to the user, then passes DeviceCode,
// Interval, and Expiry to [Client.PollToken].
type DeviceAuth struct {
	// VerificationURIComplete is the URL the user opens to approve the login.
	VerificationURIComplete string
	// DeviceCode authenticates the subsequent token poll.
	DeviceCode string
	// Interval is the minimum delay between token polls.
	Interval time.Duration
	// Expiry is the absolute time after which DeviceCode is no longer valid.
	Expiry time.Time
}

// StartDeviceAuth begins the device authorization grant by requesting a device
// code and user verification URL. It performs no logging; the caller presents
// the returned VerificationURIComplete to the user.
func (c *Client) StartDeviceAuth(ctx context.Context) (DeviceAuth, error) {
	form := url.Values{}
	form.Set(paramClientID, c.clientID)
	form.Set(paramScope, scope)

	endpoint := c.baseURL + devicePath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return DeviceAuth{}, fmt.Errorf("auth: build device authorization request: %w", err)
	}
	req.Header.Set(contentTypeHeader, formContentType)
	req.Header.Set(acceptHeader, jsonMediaType)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return DeviceAuth{}, fmt.Errorf("auth: device authorization request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return DeviceAuth{}, fmt.Errorf("auth: device authorization status %d: %w", resp.StatusCode, errUnexpectedStatus)
	}

	var body struct {
		DeviceCode              string `json:"deviceCode"`
		VerificationURIComplete string `json:"verificationUriComplete"`
		ExpiresIn               int64  `json:"expiresIn"`
		Interval                int64  `json:"interval"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, responseBodyLimit)).Decode(&body); err != nil {
		return DeviceAuth{}, fmt.Errorf("auth: decode device authorization response: %w", err)
	}

	return DeviceAuth{
		VerificationURIComplete: body.VerificationURIComplete,
		DeviceCode:              body.DeviceCode,
		Interval:                time.Duration(body.Interval) * time.Second,
		Expiry:                  c.now().Add(time.Duration(body.ExpiresIn) * time.Second),
	}, nil
}

// PollToken polls the token endpoint until the user approves the login, the
// device code expires, or ctx is done. On success it persists the token through
// the store and returns nil. It honors authorization_pending and slow_down per
// RFC 8628, growing interval by the configured increment on slow_down.
func (c *Client) PollToken(ctx context.Context, deviceCode string, interval time.Duration, expiry time.Time) error {
	for {
		if !c.now().Before(expiry) {
			return ErrDeviceCodeExpired
		}

		tr, err := c.pollOnce(ctx, deviceCode)
		switch {
		case err == nil:
			if saveErr := c.store.Save(ctx, c.tokenFromResponse(tr)); saveErr != nil {
				return fmt.Errorf("auth: save token: %w", saveErr)
			}
			return nil
		case errors.Is(err, errAuthorizationPending):
			// Keep polling at the current interval.
		case errors.Is(err, errSlowDown):
			interval += c.slowDownIncrease
		default:
			return err
		}

		if err = sleepOrDone(ctx, interval); err != nil {
			return err
		}
	}
}

// pollOnce performs a single device-code token request.
func (c *Client) pollOnce(ctx context.Context, deviceCode string) (tokenResponse, error) {
	form := url.Values{}
	form.Set(paramClientID, c.clientID)
	form.Set(paramScope, scope)
	form.Set(paramGrantType, deviceCodeGrant)
	form.Set(paramDeviceCode, deviceCode)

	return c.postToken(ctx, form)
}

// sleepOrDone waits for d or until ctx is done, whichever comes first.
func sleepOrDone(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("auth: poll wait: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}
