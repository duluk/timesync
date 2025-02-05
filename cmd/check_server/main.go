package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const (
	defaultPort  = 13337
	readTimeout  = 5 * time.Second
	writeTimeout = 5 * time.Second
)

var (
	port    = flag.Int("port", defaultPort, "Port to listen on")
	verbose = flag.Bool("verbose", false, "Enable verbose logging")
	daemon  = flag.Bool("daemon", false, "Run as daemon")
	pidFile = flag.String("pidfile", "/var/run/timesync.pid", "PID file path")
	logFile = flag.String("logfile", "/var/log/timesync.log", "Log file path when running as daemon")
)

func daemonize() {
	exe, err := os.Executable()
	if err != nil {
		log.Fatalf("Failed to get executable path: %v", err)
	}

	exePath, err := filepath.Abs(exe)
	if err != nil {
		log.Fatalf("Failed to get absolute path: %v", err)
	}

	args := []string{}
	for _, arg := range os.Args[1:] {
		if arg != "-daemon" {
			args = append(args, arg)
		}
	}

	cmd := exec.Command(exePath, args...)
	cmd.Start()

	fmt.Printf("Daemon process started with PID %d\n", cmd.Process.Pid)
	os.Exit(0)
}

func writePidFile(pidFile string) error {
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0644); err != nil {
		return fmt.Errorf("failed to write PID file: %v", err)
	}
	return nil
}

func setupLogging(logFile string) (*os.File, error) {
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	file, err := os.OpenFile(logFile, flags, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %v", err)
	}

	log.SetOutput(file)
	log.SetFlags(log.Ldate | log.Ltime | log.LUTC)
	return file, nil
}

func main() {
	flag.Parse()

	if *daemon {
		daemonize()
	}

	var logFile *os.File
	if *daemon {
		var err error
		logFile, err = setupLogging("./check_server.log")
		if err != nil {
			log.Fatalf("Failed to set up logging: %v", err)
		}
		defer logFile.Close()
	}

	if err := writePidFile(*pidFile); err != nil {
		log.Fatalf("Failed to write PID file: %v", err)
	}
	defer os.Remove(*pidFile)

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("Failed to create listener: %v", err)
	}
	defer listener.Close()

	log.Printf("Time sync server listening on port %d", *port)

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup
	connections := make(map[net.Conn]struct{})
	var connMutex sync.Mutex

	go func() {
		<-shutdown
		log.Println("Shutdown signal received, closing listener...")
		listener.Close()

		connMutex.Lock()
		for conn := range connections {
			conn.Close()
		}
		connMutex.Unlock()

		wg.Wait()
		log.Println("Server shutdown complete")
		os.Exit(0)
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-shutdown:
				return
			default:
				log.Printf("Error accepting connection: %v", err)
				continue
			}
		}

		connMutex.Lock()
		connections[conn] = struct{}{}
		connMutex.Unlock()

		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			defer func() {
				connMutex.Lock()
				delete(connections, c)
				connMutex.Unlock()
				c.Close()
			}()

			handleConnection(c)
		}(conn)
	}
}

func handleConnection(conn net.Conn) {
	remoteAddr := conn.RemoteAddr().String()
	if *verbose {
		log.Printf("New connection from %s", remoteAddr)
	}

	buffer := make([]byte, 1024)

	for {
		if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
			log.Printf("Failed to set read deadline for %s: %v", remoteAddr, err)
			return
		}

		_, err := conn.Read(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				if *verbose {
					log.Printf("Read timeout from %s", remoteAddr)
				}
			} else if err.Error() != "EOF" {
				log.Printf("Error reading from %s: %v", remoteAddr, err)
			}
			return
		}

		if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
			log.Printf("Failed to set write deadline for %s: %v", remoteAddr, err)
			return
		}

		response := fmt.Sprintf("%d\n", time.Now().UnixNano())
		if _, err := conn.Write([]byte(response)); err != nil {
			log.Printf("Error writing to %s: %v", remoteAddr, err)
			return
		}

		if *verbose {
			log.Printf("Processed time sync request from %s", remoteAddr)
		}
	}
}
