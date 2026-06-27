package tidal

import (
	"net/http"
	"strconv"
	"time"

	"github.com/hashicorp/go-retryablehttp"
)

// Backoff is a [retryablehttp.Backoff] policy. On a 429 (Too Many Requests)
// response carrying a Retry-After header it waits exactly the advertised delay,
// clamped so it never exceeds waitMax. For every other case it delegates to
// [retryablehttp.DefaultBackoff], which applies exponential backoff between
// waitMin and waitMax.
func Backoff(waitMin, waitMax time.Duration, attemptNum int, resp *http.Response) time.Duration {
	if delay, ok := retryAfter(resp); ok {
		return min(delay, waitMax)
	}
	return retryablehttp.DefaultBackoff(waitMin, waitMax, attemptNum, resp)
}

// retryAfter extracts the Retry-After delay from a 429 response. It understands
// both the delta-seconds and the HTTP-date forms defined by RFC 7231. The bool
// is false when the response is not a 429, the header is absent, or the value
// cannot be parsed into a non-negative delay.
func retryAfter(resp *http.Response) (time.Duration, bool) {
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		return 0, false
	}

	header := resp.Header.Get("Retry-After")
	if header == "" {
		return 0, false
	}

	if secs, err := strconv.Atoi(header); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}

	if when, err := http.ParseTime(header); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay, true
		}
	}
	return 0, false
}
