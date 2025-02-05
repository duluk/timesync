package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

const (
	timePort            = 13337
	connectionTimeout   = 5 * time.Second
	samplesPerHost      = 5
	acceptableThreshold = 500 * time.Millisecond
)

type HostResult struct {
	Hostname  string
	MinOffset time.Duration
	MaxOffset time.Duration
	AvgOffset time.Duration
	RTT       time.Duration
	Error     error
	Reachable bool
}

func main() {
	flag.Parse()
	hosts := flag.Args()

	if len(hosts) < 2 {
		fmt.Println("Usage: timesync host1 host2 [host3 ...]")
		fmt.Println("Please provide at least two hostnames to compare")
		os.Exit(1)
	}

	results := checkHosts(hosts)
	printResults(results)
}

func checkHosts(hosts []string) []HostResult {
	results := make([]HostResult, len(hosts))
	var wg sync.WaitGroup

	for i, host := range hosts {
		wg.Add(1)
		go func(index int, hostname string) {
			defer wg.Done()
			results[index] = checkHost(hostname)
		}(i, host)
	}

	wg.Wait()
	return results
}

func checkHost(hostname string) HostResult {
	result := HostResult{
		Hostname:  hostname,
		MinOffset: time.Duration(1<<63 - 1),
		MaxOffset: time.Duration(-1 << 63),
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectionTimeout)
	defer cancel()

	dialer := &net.Dialer{
		Timeout: connectionTimeout,
	}

	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", hostname, timePort))
	if err != nil {
		result.Error = fmt.Errorf("connection failed: %v", err)
		return result
	}
	defer conn.Close()

	result.Reachable = true

	var totalOffset time.Duration
	var totalRTT time.Duration

	for i := 0; i < samplesPerHost; i++ {
		t0 := time.Now()

		fmt.Fprintf(conn, "%d\n", t0.UnixNano())

		var remoteTime int64
		fmt.Fscanf(conn, "%d\n", &remoteTime)

		t1 := time.Now()
		rtt := t1.Sub(t0)

		offset := time.Duration(remoteTime - t0.Add(rtt/2).UnixNano())

		if offset < result.MinOffset {
			result.MinOffset = offset
		}
		if offset > result.MaxOffset {
			result.MaxOffset = offset
		}

		totalOffset += offset
		totalRTT += rtt

		time.Sleep(100 * time.Millisecond)
	}

	result.AvgOffset = totalOffset / samplesPerHost
	result.RTT = totalRTT / samplesPerHost
	return result
}

func printResults(results []HostResult) {
	fmt.Println("\nTime Synchronization Results:")
	fmt.Println("=============================")

	var referenceHost string
	minAvgOffset := time.Duration(1<<63 - 1)

	for _, result := range results {
		if result.Reachable && result.AvgOffset.Abs() < minAvgOffset {
			minAvgOffset = result.AvgOffset.Abs()
			referenceHost = result.Hostname
		}
	}

	fmt.Printf("Reference host: %s\n\n", referenceHost)

	for _, result := range results {
		fmt.Printf("Host: %s\n", result.Hostname)
		if !result.Reachable {
			fmt.Printf("  Status: Unreachable - %v\n", result.Error)
			continue
		}

		fmt.Printf("  Average Offset: %v\n", result.AvgOffset)
		fmt.Printf("  Min/Max Offset: %v to %v\n", result.MinOffset, result.MaxOffset)
		fmt.Printf("  Average RTT: %v\n", result.RTT)

		if result.AvgOffset.Abs() > acceptableThreshold {
			fmt.Printf("  WARNING: Time difference exceeds acceptable threshold of %v\n", acceptableThreshold)
		}
		fmt.Println()
	}
}
