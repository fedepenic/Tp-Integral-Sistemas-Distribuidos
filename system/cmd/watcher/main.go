package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	dockerSocket        = "/var/run/docker.sock"
	registrationPort    = "8888"
)

// ServiceTarget is a service the watcher monitors.
type ServiceTarget struct {
	name string // Docker Compose service name (used to find the container)
	host string // hostname reachable within the Docker network (usually == name)
	port string // TCP port to ping for liveness
}

// Container is a trimmed-down Docker API list response entry.
type Container struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseInterval(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 15 * time.Second
	}
	return d
}

func mustInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		log.Fatalf("[watcher] %s must be a non-negative integer, got %q", key, v)
	}
	return n
}

// parseServices parses SERVICES env var entries in the form "name:port" or
// "name:host:port". Example: "rabbitmq:5672,gateway:8080,cleaner_1:9999"
func parseServices(s string) []ServiceTarget {
	var targets []ServiceTarget
	for _, entry := range strings.Split(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.Split(entry, ":")
		switch len(parts) {
		case 2:
			targets = append(targets, ServiceTarget{name: parts[0], host: parts[0], port: parts[1]})
		case 3:
			targets = append(targets, ServiceTarget{name: parts[0], host: parts[1], port: parts[2]})
		default:
			log.Printf("[watcher] WARNING: ignoring malformed service entry %q", entry)
		}
	}
	return targets
}

// waitForRegistrations blocks until n services have connected to the
// registration port and announced themselves. This replaces any fixed startup
// delay: monitoring only begins once every expected service is known to be up.
func waitForRegistrations(n int) {
	if n == 0 {
		log.Printf("[watcher] N_SERVICES=0, skipping registration wait")
		return
	}

	ln, err := net.Listen("tcp", ":"+registrationPort)
	if err != nil {
		log.Fatalf("[watcher] could not open registration port :%s: %v", registrationPort, err)
	}
	defer ln.Close()

	log.Printf("[watcher] registration server listening on :%s, waiting for %d service(s)...", registrationPort, n)

	registered := make(map[string]bool)
	for len(registered) < n {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("[watcher] registration accept error: %v", err)
			continue
		}

		name, err := bufio.NewReader(conn).ReadString('\n')
		conn.Close()
		name = strings.TrimSpace(name)
		if err != nil && name == "" {
			log.Printf("[watcher] could not read registration name: %v", err)
			continue
		}

		if !registered[name] {
			registered[name] = true
			log.Printf("[watcher] REGISTERED %q (%d/%d)", name, len(registered), n)
		} else {
			log.Printf("[watcher] duplicate registration from %q (ignored)", name)
		}
	}

	log.Printf("[watcher] all %d service(s) registered — starting health monitoring", n)
}

// newDockerClient returns an HTTP client that talks to the Docker daemon via
// the Unix socket. Used only for restarting containers, never for liveness checks.
func newDockerClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", dockerSocket)
			},
		},
		Timeout: 10 * time.Second,
	}
}

// pingTCP tries to open a TCP connection to host:port. Does not use Docker.
func pingTCP(host, port string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// findContainerID queries Docker for a container belonging to the given compose
// project and service name. Docker is only used here to obtain the ID for restart.
func findContainerID(client *http.Client, project, service string) (string, error) {
	filters := fmt.Sprintf(
		`{"label":["com.docker.compose.project=%s","com.docker.compose.service=%s"]}`,
		project, service,
	)
	reqURL := "http://localhost/containers/json?all=1&filters=" + url.QueryEscape(filters)

	resp, err := client.Get(reqURL)
	if err != nil {
		return "", fmt.Errorf("list containers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("list containers: status %d", resp.StatusCode)
	}

	var containers []Container
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return "", fmt.Errorf("decode containers: %w", err)
	}

	if len(containers) == 0 {
		return "", fmt.Errorf("no container found for service=%s in project=%s", service, project)
	}

	id := containers[0].ID
	log.Printf("[watcher]   container id for %s: %s", service, id[:12])
	return id, nil
}

// restartContainer sends POST /containers/{id}/start to the Docker daemon.
func restartContainer(client *http.Client, id string) error {
	resp, err := client.Post(
		fmt.Sprintf("http://localhost/containers/%s/start", id),
		"application/json",
		nil,
	)
	if err != nil {
		return fmt.Errorf("docker start: %w", err)
	}
	defer resp.Body.Close()

	// 204 = started, 304 = already running.
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified {
		return fmt.Errorf("docker start returned unexpected status %d", resp.StatusCode)
	}
	return nil
}

func checkAll(dockerClient *http.Client, project string, services []ServiceTarget, pingTimeout time.Duration) {
	log.Printf("[watcher] --- health check round (project=%s, services=%d) ---", project, len(services))

	alive, dead, failed := 0, 0, 0

	for _, svc := range services {
		addr := net.JoinHostPort(svc.host, svc.port)
		log.Printf("[watcher] PING   %s @ %s ...", svc.name, addr)

		if pingTCP(svc.host, svc.port, pingTimeout) {
			log.Printf("[watcher] OK     %s — responded on %s", svc.name, addr)
			alive++
			continue
		}

		log.Printf("[watcher] DOWN   %s — no response on %s", svc.name, addr)
		dead++

		log.Printf("[watcher]   looking up container for service=%s ...", svc.name)
		id, err := findContainerID(dockerClient, project, svc.name)
		if err != nil {
			log.Printf("[watcher]   ERROR could not find container for %s: %v", svc.name, err)
			failed++
			continue
		}

		log.Printf("[watcher]   restarting container %s (id=%s) ...", svc.name, id[:12])
		if err := restartContainer(dockerClient, id); err != nil {
			log.Printf("[watcher]   ERROR could not restart %s: %v", svc.name, err)
			failed++
		} else {
			log.Printf("[watcher]   UP    %s restarted successfully", svc.name)
		}
	}

	log.Printf("[watcher] --- round done: alive=%d restarted=%d failed=%d ---", alive, dead-failed, failed)
}

func main() {
	project     := envOrDefault("COMPOSE_PROJECT", "system")
	interval    := parseInterval(envOrDefault("WATCH_INTERVAL", "15s"))
	pingTimeout := parseInterval(envOrDefault("PING_TIMEOUT", "3s"))
	nServices   := mustInt("N_SERVICES", 0)
	servicesRaw := envOrDefault("SERVICES", "")

	log.Printf("[watcher] ============================================================")
	log.Printf("[watcher] watcher starting")
	log.Printf("[watcher] compose project  : %s", project)
	log.Printf("[watcher] check interval   : %s", interval)
	log.Printf("[watcher] ping timeout     : %s", pingTimeout)
	log.Printf("[watcher] expected services: %d", nServices)
	log.Printf("[watcher] ============================================================")

	if servicesRaw == "" {
		log.Fatalf("[watcher] SERVICES env var is required (e.g. rabbitmq:5672,gateway:8080,cleaner_1:9999)")
	}

	services := parseServices(servicesRaw)
	if len(services) == 0 {
		log.Fatalf("[watcher] no valid service entries parsed from SERVICES=%q", servicesRaw)
	}

	log.Printf("[watcher] monitoring %d service(s):", len(services))
	for _, svc := range services {
		log.Printf("[watcher]   - %s @ %s:%s", svc.name, svc.host, svc.port)
	}

	// Block here until every expected service has registered.
	// This replaces any fixed startup delay.
	waitForRegistrations(nServices)

	dockerClient := newDockerClient()

	for {
		checkAll(dockerClient, project, services, pingTimeout)
		log.Printf("[watcher] next check in %s", interval)
		time.Sleep(interval)
	}
}
