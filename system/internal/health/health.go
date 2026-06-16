package health

import (
	"log"
	"net"
	"os"
	"strings"
)

// StartIfEnabled opens a TCP listener on HEALTH_PORT when ENABLE_WATCHER is true.
func StartIfEnabled() {
	if !enabled() {
		return
	}

	port := os.Getenv("HEALTH_PORT")
	if port == "" {
		return
	}

	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Printf("[health] server could not listen on :%s: %v", port, err)
		return
	}

	log.Printf("[health] server listening on :%s", port)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				log.Printf("[health] server accept error: %v", err)
				return
			}
			conn.Close()
		}
	}()
}

func enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ENABLE_WATCHER"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
