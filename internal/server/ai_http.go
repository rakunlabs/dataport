package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// Requests to the AI services go through a client per proxy, so an admin
// can send them through a proxy the rest of the server does not use.

const (
	// aiRetryBase is the first wait before a retry; it doubles each time.
	aiRetryBase = 500 * time.Millisecond
	// aiRetryMax caps a wait, a Retry-After the service asks for included.
	aiRetryMax = 20 * time.Second
)

var (
	aiClientsMu sync.Mutex
	aiClients   = map[string]*http.Client{}
)

// aiClient is the client for a proxy URL; empty takes the environment's
// HTTPS_PROXY / HTTP_PROXY / NO_PROXY, as http.DefaultTransport does.
func aiClient(proxy string) *http.Client {
	aiClientsMu.Lock()
	defer aiClientsMu.Unlock()

	if c, ok := aiClients[proxy]; ok {
		return c
	}

	t := http.DefaultTransport.(*http.Transport).Clone()
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			t.Proxy = http.ProxyURL(u)
		}
	}

	c := &http.Client{Transport: t}
	aiClients[proxy] = c

	return c
}

// envProxy is the proxy the environment gives for an address, shown to the
// admin as what an empty proxy means. Credentials in it are not shown.
func envProxy(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		u = &url.URL{Scheme: "https", Host: "api.openai.com"}
	}

	p, err := http.ProxyFromEnvironment(&http.Request{URL: u})
	if err != nil || p == nil {
		return ""
	}

	p.User = nil

	return p.String()
}

// aiDo sends a request, trying again up to retries times when the service
// cannot be reached or answers 429 or 5xx, with a growing wait or the one
// its Retry-After asks for. Only an answer that has not begun is tried
// again: a non-200 answer comes back as an error, a 200 to the caller.
func aiDo(ctx context.Context, client *http.Client, req *http.Request, body []byte, retries int) (*http.Response, error) {
	var last error

	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			req.Body = io.NopCloser(bytes.NewReader(body))
		}

		resp, err := client.Do(req)

		var wait time.Duration

		switch {
		case err != nil:
			if ctx.Err() != nil {
				return nil, fmt.Errorf("cannot reach the AI service: %w", err)
			}

			last = fmt.Errorf("cannot reach the AI service: %w", err)
		case resp.StatusCode == http.StatusOK:
			return resp, nil
		default:
			data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			_ = resp.Body.Close()

			last = fmt.Errorf("the AI service answered %d: %s", resp.StatusCode, aiErrorText(data))
			if !aiRetryable(resp.StatusCode) {
				return nil, last
			}

			wait = retryAfter(resp.Header.Get("Retry-After"))
		}

		if attempt >= retries {
			if attempt > 0 {
				return nil, fmt.Errorf("%w (after %d tries)", last, attempt+1)
			}

			return nil, last
		}

		if wait == 0 {
			// Exponential with jitter: 0.5s, 1s, 2s … from half to the full step.
			step := aiRetryBase << attempt
			wait = step/2 + rand.N(step/2+1)
		}

		wait = min(wait, aiRetryMax)

		select {
		case <-ctx.Done():
			return nil, errors.Join(last, ctx.Err())
		case <-time.After(wait):
		}
	}
}

func aiRetryable(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusRequestTimeout || status >= 500
}

// retryAfter reads a Retry-After header in seconds or as a date.
func retryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}

	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}

	if t, err := http.ParseTime(v); err == nil {
		return max(time.Until(t), 0)
	}

	return 0
}

// maskURL is a URL with its password shown as the mask, for a form.
func maskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || raw == "" {
		return raw
	}

	if u.User != nil {
		if _, ok := u.User.Password(); ok {
			u.User = url.UserPassword(u.User.Username(), proxyMask)
		}
	}

	return u.String()
}

// unmaskURL puts the saved password back into a URL the form sent with the
// mask, when host and user are the saved ones. Otherwise the URL is
// returned as it is, and a mask left in it is refused by the caller.
func unmaskURL(raw, saved string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}

	if p, ok := u.User.Password(); !ok || p != proxyMask {
		return raw
	}

	old, err := url.Parse(saved)
	if err != nil || saved == "" || old.Host != u.Host || old.User == nil || old.User.Username() != u.User.Username() {
		return raw
	}

	u.User = old.User

	return u.String()
}
