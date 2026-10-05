// Command loadtest exercises read-only HTTP endpoints with bounded concurrency,
// as a burst (high concurrency, short) or a soak (moderate concurrency, long).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	requestTimeout = 10 * time.Second
	maxBodyBytes   = 1 << 20
)

type config struct {
	targets     []string
	duration    time.Duration
	concurrency int
	cookie      string
}

type result struct {
	Requests          int64   `json:"requests"`
	Failed            int64   `json:"failed"`
	Concurrency       int     `json:"concurrency"`
	Seconds           float64 `json:"seconds"`
	RequestsPerSecond float64 `json:"requests_per_second"`
	P50ms             int     `json:"p50_ms"`
	P95ms             int     `json:"p95_ms"`
	P99ms             int     `json:"p99_ms"`
}

func parseTargets(list string) ([]string, error) {
	var targets []string
	for target := range strings.SplitSeq(list, ",") {
		u, err := url.Parse(strings.TrimSpace(target))
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return nil, fmt.Errorf("invalid target URL %q", target)
		}
		targets = append(targets, u.String())
	}
	return targets, nil
}

// load spreads requests round-robin across targets. A request fails on a
// transport error, a non-200 status, a timeout, or a body over maxBodyBytes.
func load(ctx context.Context, cfg config) result {
	transport := &http.Transport{MaxIdleConns: cfg.concurrency, MaxIdleConnsPerHost: cfg.concurrency, MaxConnsPerHost: cfg.concurrency, IdleConnTimeout: 30 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: requestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	var next, requests, failed atomic.Int64
	var milliseconds [int(requestTimeout/time.Millisecond) + 1]atomic.Int64
	start := time.Now()
	deadline := start.Add(cfg.duration)
	var wg sync.WaitGroup
	for range cfg.concurrency {
		wg.Go(func() {
			for time.Now().Before(deadline) && ctx.Err() == nil {
				target := cfg.targets[int(next.Add(1)-1)%len(cfg.targets)]
				began := time.Now()
				ok := fetch(ctx, client, target, cfg.cookie)
				milliseconds[min(time.Since(began).Milliseconds(), int64(len(milliseconds)-1))].Add(1)
				requests.Add(1)
				if !ok {
					failed.Add(1)
				}
			}
		})
	}
	wg.Wait()

	elapsed := time.Since(start).Seconds()
	total := requests.Load()
	percentile := func(percent int64) int {
		threshold := (total*percent + 99) / 100
		var count int64
		for ms := range milliseconds {
			if count += milliseconds[ms].Load(); count >= threshold {
				return ms
			}
		}
		return len(milliseconds) - 1
	}
	return result{Requests: total, Failed: failed.Load(), Concurrency: cfg.concurrency, Seconds: elapsed,
		RequestsPerSecond: float64(total) / elapsed, P50ms: percentile(50), P95ms: percentile(95), P99ms: percentile(99)}
}

func fetch(ctx context.Context, client *http.Client, target, cookie string) bool {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	response, err := client.Do(req)
	if err != nil {
		return false
	}
	count, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, maxBodyBytes+1))
	closeErr := response.Body.Close()
	return response.StatusCode == http.StatusOK && readErr == nil && closeErr == nil && count <= maxBodyBytes
}

func run() error {
	targets := flag.String("url", "http://127.0.0.1:8002/donor/public/content", "comma-separated read-only endpoints, requested round-robin")
	duration := flag.Duration("duration", time.Minute, "test duration (up to 1h)")
	concurrency := flag.Int("concurrency", 16, "concurrent requests (1–512)")
	maxP99 := flag.Duration("max-p99", 0, "fail when p99 latency exceeds this budget (0 disables)")
	flag.Parse()
	parsed, err := parseTargets(*targets)
	if err != nil || *duration <= 0 || *duration > time.Hour || *concurrency < 1 || *concurrency > 512 || *maxP99 < 0 {
		return errors.New("invalid load-test arguments")
	}
	r := load(context.Background(), config{targets: parsed, duration: *duration, concurrency: *concurrency, cookie: os.Getenv("DONOR_LOAD_COOKIE")})
	if err := json.NewEncoder(os.Stdout).Encode(r); err != nil {
		return err
	}
	switch {
	case r.Requests == 0 || r.Failed != 0:
		return errors.New("load test encountered failed requests")
	case *maxP99 > 0 && time.Duration(r.P99ms)*time.Millisecond > *maxP99:
		return fmt.Errorf("p99 latency %dms exceeded the %s budget", r.P99ms, *maxP99)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
