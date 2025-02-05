package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type MachineTime struct {
	Hostname string
	Time     time.Time
	Offset   time.Duration
	Error    error
}

const (
	ntpEpochOffset    = 2208988800
	warningThreshold  = 1 * time.Second
	criticalThreshold = 5 * time.Second
)

func getNTPTime(hostname string) (time.Time, error) {
	log.Printf("Initiating NTP connection to %s", hostname)
	conn, err := net.Dial("udp", hostname+":123")
	if err != nil {
		return time.Time{}, fmt.Errorf("NTP dial failed: %v", err)
	}
	defer conn.Close()

	// NTP request packet
	req := make([]byte, 48)
	req[0] = 0x1B // Version 3 + Mode 3

	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return time.Time{}, fmt.Errorf("failed to set deadline: %v", err)
	}

	if _, err := conn.Write(req); err != nil {
		return time.Time{}, fmt.Errorf("failed to send NTP request: %v", err)
	}

	resp := make([]byte, 48)
	if _, err := conn.Read(resp); err != nil {
		return time.Time{}, fmt.Errorf("failed to read NTP response: %v", err)
	}
	log.Printf("Received NTP response from %s", hostname)

	// Extract timestamp from response (bytes 40-43 contain the timestamp)
	secs := binary.BigEndian.Uint32(resp[40:44])
	// Convert NTP timestamp to Unix time
	ntpTime := time.Unix(int64(secs-ntpEpochOffset), 0)

	return ntpTime, nil
}

func getTimeFromMachine(hostname string) (time.Time, error) {
	// Try NTP first
	ntpTime, err := getNTPTime(hostname)
	if err == nil {
		return ntpTime, nil
	}
	log.Printf("NTP failed for %s, falling back to SSH: %v", hostname, err)

	// Fall back to SSH
	return getSSHTime(hostname)
}

func getSSHTime(hostname string) (time.Time, error) {
	sshAgent, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK"))
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to connect to SSH agent: %v", err)
	}
	defer sshAgent.Close()

	agentClient := agent.NewClient(sshAgent)
	config := &ssh.ClientConfig{
		User: os.Getenv("USER"),
		Auth: []ssh.AuthMethod{
			ssh.PublicKeysCallback(agentClient.Signers),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	client, err := ssh.Dial("tcp", hostname+":22", config)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to dial SSH: %v", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to create SSH session: %v", err)
	}
	defer session.Close()

	output, err := session.Output("date -u '+%Y-%m-%d %H:%M:%S.%N %z'")
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to run command: %v", err)
	}

	t, err := time.Parse("2006-01-02 15:04:05.999999999 -0700", strings.TrimSpace(string(output)))
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse time: %v", err)
	}

	return t, nil
}

// Modify the main function to use getTimeFromMachine instead of getNTPTime
func setupLogging() (*os.File, error) {
	// Determine log directory
	logDir := os.Getenv("XDG_STATE_HOME")
	if logDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("could not find home directory: %v", err)
		}
		logDir = filepath.Join(homeDir, ".local", "state")
	}

	// Ensure log directory exists
	logDir = filepath.Join(logDir, "timesync")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("could not create log directory: %v", err)
	}

	// Open log file with timestamp in name
	timestamp := time.Now().Format("2006-01-02")
	logPath := filepath.Join(logDir, fmt.Sprintf("timesync-%s.log", timestamp))
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("could not open log file: %v", err)
	}

	log.SetOutput(logFile)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.LUTC)

	log.Printf("Logging initialized to: %s", logPath)
	return logFile, nil
}

func main() {
	// Set up logging first thing
	logFile, err := setupLogging()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set up logging: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()

	if len(os.Args) < 2 {
		fmt.Println("Usage: go run clock_sync.go <hostname1> <hostname2> ...")
		os.Exit(1)
	}

	log.Printf("Starting clock synchronization check for %d hosts", len(os.Args)-1)
	hostnames := os.Args[1:]
	machineTimes := make([]MachineTime, 0, len(hostnames))
	var mu sync.Mutex
	var wg sync.WaitGroup

	referenceTime := time.Now()

	for _, hostname := range hostnames {
		wg.Add(1)
		go func(host string) {
			defer wg.Done()
			log.Printf("Checking time for host: %s", host)
			time, err := getTimeFromMachine(host) // Changed this line
			mt := MachineTime{
				Hostname: host,
				Time:     time,
				Error:    err,
			}
			if err == nil {
				mt.Offset = time.Sub(referenceTime)
				log.Printf("Successfully got time from %s, offset: %v", host, mt.Offset)
			} else {
				log.Printf("Failed to get time from %s: %v", host, err)
			}

			mu.Lock()
			machineTimes = append(machineTimes, mt)
			mu.Unlock()
		}(hostname)
	}

	wg.Wait()

	sort.Slice(machineTimes, func(i, j int) bool {
		return machineTimes[i].Offset < machineTimes[j].Offset
	})

	fmt.Println("Clock Synchronization Results:")
	fmt.Println("==============================")
	for _, mt := range machineTimes {
		fmt.Printf("%s: %v (offset: %v)\n", mt.Hostname, mt.Time.Format(time.RFC3339Nano), mt.Offset)
	}

	if len(machineTimes) > 1 {
		maxDrift := machineTimes[len(machineTimes)-1].Offset - machineTimes[0].Offset
		fmt.Printf("\nMaximum clock drift between machines: %v\n", maxDrift)

		if maxDrift > criticalThreshold {
			fmt.Printf("CRITICAL: Clock drift exceeds %v\n", criticalThreshold)
		} else if maxDrift > warningThreshold {
			fmt.Printf("WARNING: Clock drift exceeds %v\n", warningThreshold)
		} else {
			fmt.Printf("OK: Clock drift is within acceptable range\n")
		}
	}
}
