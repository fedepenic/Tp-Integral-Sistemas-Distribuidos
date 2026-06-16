package main

import (
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

const dockerSocket = "/var/run/docker.sock"

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
	Status string            `json:"Status"`
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

func parsePositiveInt(key string, def int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return def
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		log.Printf("[watcher] WARNING: invalid %s=%q, using %d", key, value, def)
		return def
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

func startHealthServer(port string) {
	if port == "" {
		return
	}
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Printf("[watcher] health server could not listen on :%s: %v", port, err)
		return
	}
	log.Printf("[watcher] health server listening on :%s", port)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				log.Printf("[watcher] health server accept error: %v", err)
				return
			}
			conn.Close()
		}
	}()
}

// findContainer queries Docker for a container belonging to the given compose
// project and service name. Docker is only used here to obtain the ID for restart.
func findContainer(client *http.Client, project, service string) (Container, error) {
	filters := fmt.Sprintf(
		`{"label":["com.docker.compose.project=%s","com.docker.compose.service=%s"]}`,
		project, service,
	)
	reqURL := "http://localhost/containers/json?all=1&filters=" + url.QueryEscape(filters)

	resp, err := client.Get(reqURL)
	if err != nil {
		return Container{}, fmt.Errorf("list containers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Container{}, fmt.Errorf("list containers: status %d", resp.StatusCode)
	}

	var containers []Container
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return Container{}, fmt.Errorf("decode containers: %w", err)
	}

	if len(containers) == 0 {
		return Container{}, fmt.Errorf("no container found for service=%s in project=%s", service, project)
	}

	container := containers[0]
	log.Printf("[watcher]   container id for %s: %s", service, container.ID[:12])
	return container, nil
}

func completedSuccessfully(container Container) bool {
	return container.State == "exited" && strings.HasPrefix(container.Status, "Exited (0)")
}

// restartContainer sends POST /containers/{id}/restart to the Docker daemon.
func restartContainer(client *http.Client, id string) error {
	resp, err := client.Post(
		fmt.Sprintf("http://localhost/containers/%s/restart", id),
		"application/json",
		nil,
	)
	if err != nil {
		return fmt.Errorf("docker restart: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("docker restart returned unexpected status %d", resp.StatusCode)
	}
	return nil
}

func sleepUntilNextSlot(watcherID, watcherTotal int, interval time.Duration) time.Duration {
	if watcherTotal <= 1 {
		return 0
	}
	slot := interval / time.Duration(watcherTotal)
	if slot <= 0 {
		return 0
	}
	targetOffset := time.Duration(watcherID-1) * slot
	currentOffset := time.Duration(time.Now().UnixNano()) % interval
	wait := targetOffset - currentOffset
	if wait < 0 {
		wait += interval
	}
	time.Sleep(wait)
	return wait
}

func checkAll(dockerClient *http.Client, project string, services []ServiceTarget, pingTimeout time.Duration, watcherID int) {
	log.Printf("[watcher %d] --- health check round (project=%s, services=%d) ---", watcherID, project, len(services))

	alive, dead, completed, restarted, failed := 0, 0, 0, 0, 0

	for _, svc := range services {
		addr := net.JoinHostPort(svc.host, svc.port)
		log.Printf("[watcher %d] PING   %s @ %s ...", watcherID, svc.name, addr)

		if pingTCP(svc.host, svc.port, pingTimeout) {
			log.Printf("[watcher %d] OK     %s — responded on %s", watcherID, svc.name, addr)
			alive++
			continue
		}

		log.Printf("[watcher %d] DOWN   %s — no response on %s", watcherID, svc.name, addr)
		dead++

		log.Printf("[watcher %d]   looking up container for service=%s ...", watcherID, svc.name)
		container, err := findContainer(dockerClient, project, svc.name)
		if err != nil {
			log.Printf("[watcher %d]   ERROR could not find container for %s: %v", watcherID, svc.name, err)
			failed++
			continue
		}

		if completedSuccessfully(container) {
			log.Printf("[watcher %d]   DONE  %s completed successfully; not restarting", watcherID, svc.name)
			completed++
			continue
		}

		id := container.ID
		log.Printf("[watcher %d]   restarting container %s (id=%s) ...", watcherID, svc.name, id[:12])
		if err := restartContainer(dockerClient, id); err != nil {
			log.Printf("[watcher %d]   ERROR could not restart %s: %v", watcherID, svc.name, err)
			failed++
		} else {
			log.Printf("[watcher %d]   UP    %s restarted successfully", watcherID, svc.name)
			restarted++
		}
	}

	log.Printf("[watcher %d] --- round done: alive=%d down=%d completed=%d restarted=%d failed=%d ---", watcherID, alive, dead, completed, restarted, failed)
}

func main() {
	project := envOrDefault("COMPOSE_PROJECT", "system")
	interval := parseInterval(envOrDefault("WATCH_INTERVAL", "15s"))
	pingTimeout := parseInterval(envOrDefault("PING_TIMEOUT", "3s"))
	startupDelay := parseInterval(envOrDefault("STARTUP_DELAY", "30s"))
	servicesRaw := envOrDefault("SERVICES", "")
	watcherID := parsePositiveInt("WATCHER_ID", 1)
	watcherTotal := parsePositiveInt("WATCHER_TOTAL", 1)
	healthPort := envOrDefault("HEALTH_PORT", "")

	if watcherID > watcherTotal {
		log.Printf("[watcher] WARNING: WATCHER_ID=%d is greater than WATCHER_TOTAL=%d, using WATCHER_ID=1", watcherID, watcherTotal)
		watcherID = 1
	}
	startHealthServer(healthPort)

	log.Printf("[watcher] ============================================================")
	log.Printf("[watcher] watcher starting     : %d/%d", watcherID, watcherTotal)
	log.Printf("[watcher] compose project  : %s", project)
	log.Printf("[watcher] check interval   : %s", interval)
	log.Printf("[watcher] ping timeout     : %s", pingTimeout)
	log.Printf("[watcher] startup delay    : %s", startupDelay)
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

	// Give every service a fixed window to come up before monitoring begins.
	// Nodes no longer register with the watcher; they just expose a health port.
	log.Printf("[watcher] waiting %s before starting health monitoring...", startupDelay)
	time.Sleep(startupDelay)
	log.Printf("[watcher] startup delay elapsed — starting health monitoring")

	dockerClient := newDockerClient()

	for {
		wait := sleepUntilNextSlot(watcherID, watcherTotal, interval)
		if wait > 0 {
			log.Printf("[watcher %d] aligned to slot after waiting %s", watcherID, wait)
		}
		checkAll(dockerClient, project, services, pingTimeout, watcherID)
	}
}
