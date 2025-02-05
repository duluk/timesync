package main

import (
	"fmt"
	"log"
	"net"
	"os"
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
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run clock_sync.go <hostname1> <hostname2> ...")
		os.Exit(1)
	}

	hostnames := os.Args[1:]
	machineTimes := make([]MachineTime, 0, len(hostnames))
	var mu sync.Mutex
	var wg sync.WaitGroup

	referenceTime := time.Now()

	for _, hostname := range hostnames {
		wg.Add(1)
		go func(host string) {
			defer wg.Done()
			time, err := getTimeFromMachine(host)
			if err != nil {
				log.Printf("Error getting time from %s: %v", host, err)
				return
			}

			offset := time.Sub(referenceTime)
			mu.Lock()
			machineTimes = append(machineTimes, MachineTime{
				Hostname: host,
				Time:     time,
				Offset:   offset,
			})
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
	}
}

func getTimeFromMachine(hostname string) (time.Time, error) {
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
	}

	client, err := ssh.Dial("tcp", hostname+":22", config)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to dial: %v", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to create session: %v", err)
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
