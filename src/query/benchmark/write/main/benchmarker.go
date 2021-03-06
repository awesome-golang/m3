// Copyright (c) 2018 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

var stat = new(stats)

type stats struct {
	Writes    int64 `json:"writes"`
	RunTimeMs int64 `json:"run_time_ms"`
}

func (s *stats) getWrites() int64 {
	return atomic.LoadInt64(&s.Writes)
}

func (s *stats) incWrites() {
	atomic.AddInt64(&s.Writes, 1)
}

func (s *stats) getRunTimeMs() int64 {
	return atomic.LoadInt64(&s.Writes)
}

func (s *stats) setRunTimeMs(v int64) {
	atomic.StoreInt64(&s.RunTimeMs, v)
}

func (s *stats) snapshot() stats {
	return stats{Writes: s.getWrites(), RunTimeMs: s.getRunTimeMs()}
}

// HTTPClientOptions specify HTTP Client options.
type HTTPClientOptions struct {
	RequestTimeout      time.Duration `yaml:"requestTimeout"`
	ConnectTimeout      time.Duration `yaml:"connectTimeout"`
	KeepAlive           time.Duration `yaml:"keepAlive"`
	MaxIdleConnsPerHost int           `yaml:"maxIdleConnsPerHost"`
	DisableCompression  bool          `yaml:"disableCompression"`
}

// NewHTTPClient constructs a new HTTP Client.
func NewHTTPClient(o HTTPClientOptions) *http.Client {
	return &http.Client{
		Timeout: o.RequestTimeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			Dial: (&net.Dialer{
				Timeout:   o.ConnectTimeout,
				KeepAlive: o.KeepAlive,
			}).Dial,
			TLSHandshakeTimeout: o.ConnectTimeout,
			MaxIdleConnsPerHost: o.MaxIdleConnsPerHost,
			DisableCompression:  o.DisableCompression,
		},
	}
}

// DefaultHTTPClientOptions returns default options.
func DefaultHTTPClientOptions() HTTPClientOptions {
	return HTTPClientOptions{
		RequestTimeout:      2 * time.Second,
		ConnectTimeout:      2 * time.Second,
		KeepAlive:           60 * time.Second,
		MaxIdleConnsPerHost: 20,
		DisableCompression:  true,
	}
}

type benchmarker struct {
	address      string
	benchmarkers string
}

type health struct {
	Up bool `json:"up"`
}

func (b *benchmarker) serve() {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(health{Up: true})
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(stat.snapshot())
	})
	http.ListenAndServe(b.address, mux)
	if err := http.ListenAndServe(b.address, mux); err != nil {
		fmt.Fprintf(os.Stderr, "server could not listen on %s: %v", b.address, err)
	}
}

func (b *benchmarker) allAddresses() []string {
	var all []string
	for _, addr := range strings.Split(b.benchmarkers, ",") {
		addr = strings.TrimSpace(addr)
		if addr != "" {
			all = append(all, addr)
		}
	}
	return all
}

func (b *benchmarker) waitForBenchmarkers() {
	client := NewHTTPClient(DefaultHTTPClientOptions())
	allUp := false
	for !allUp {
		func() {
			// To be able to use defer run in own fn
			time.Sleep(10 * time.Millisecond)
			allUp = true
			for _, addr := range b.allAddresses() {
				req, err := http.NewRequest("GET", fmt.Sprintf("http://%s/health", addr), nil)
				if err != nil {
					panic(err)
				}

				resp, err := client.Do(req)
				if err != nil {
					allUp = false
					continue
				}

				defer resp.Body.Close()

				var r health
				if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
					fmt.Fprintf(os.Stderr, "failed to decode response from benchmarker %s: %v", addr, err)
					allUp = false
					continue
				}

				allUp = allUp && r.Up
			}
		}()
	}

	log.Println("all ready, now synchronizing to nearest 10s...")
	sync := 5 * time.Second
	now := time.Now()
	waitFor := now.Truncate(sync).Add(sync).Sub(now)
	time.Sleep(waitFor)
}
