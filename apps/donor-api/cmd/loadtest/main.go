// Command loadtest exercises read-only HTTP endpoints with bounded concurrency.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

func run() error {
	target := flag.String("url", "http://127.0.0.1:8002/donor/public/content", "read-only endpoint")
	duration := flag.Duration("duration", time.Minute, "test duration")
	concurrency := flag.Int("concurrency", 16, "concurrent requests (1–512)")
	flag.Parse()
	u, err := url.Parse(*target)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || *duration <= 0 || *duration > time.Hour || *concurrency < 1 || *concurrency > 512 {
		return fmt.Errorf("invalid load-test arguments")
	}
	transport := &http.Transport{MaxIdleConns: *concurrency, MaxIdleConnsPerHost: *concurrency, MaxConnsPerHost: *concurrency, IdleConnTimeout: 30 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var requests, failed atomic.Int64
	var milliseconds [10001]atomic.Int64
	start := time.Now()
	deadline := start.Add(*duration)
	var wg sync.WaitGroup
	for range *concurrency {
		wg.Go(func() {
			for time.Now().Before(deadline) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, *target, nil)
				if err != nil {
					cancel()
					failed.Add(1)
					return
				}
				if cookie := os.Getenv("DONOR_LOAD_COOKIE"); cookie != "" {
					req.Header.Set("Cookie", cookie)
				}
				began := time.Now()
				response, err := client.Do(req)
				bad := err != nil
				if err == nil {
					count, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20+1))
					closeErr := response.Body.Close()
					bad = response.StatusCode != http.StatusOK || readErr != nil || closeErr != nil || count > 1<<20
				}
				cancel()
				elapsed := min(time.Since(began).Milliseconds(), 10000)
				milliseconds[elapsed].Add(1)
				requests.Add(1)
				if bad {
					failed.Add(1)
				}
			}
		})
	}
	wg.Wait()
	total := requests.Load()
	percentile := func(percent int64) int {
		threshold := (total*percent + 99) / 100
		var count int64
		for ms := range milliseconds {
			count += milliseconds[ms].Load()
			if count >= threshold {
				return ms
			}
		}
		return 10000
	}
	result := map[string]any{"requests": total, "failed": failed.Load(), "concurrency": *concurrency, "seconds": time.Since(start).Seconds(), "requests_per_second": float64(total) / time.Since(start).Seconds(), "p50_ms": percentile(50), "p95_ms": percentile(95), "p99_ms": percentile(99)}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		return err
	}
	if total == 0 || failed.Load() != 0 {
		return fmt.Errorf("load test encountered failed requests")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
