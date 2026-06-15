#!/usr/bin/env python3
"""
Kills a random service from the system's docker-compose on a timer.

Configuration via environment variables (or .env via make):
    CHAOS_INTERVAL   Seconds between kills (default: 15)
    CHAOS_SERVICES_PER_INTERVAL
                     Number of random services to kill per interval (default: 1)
    CHAOS_EXCLUDE    Comma-separated services to never kill (default: rabbitmq)

Usage:
    make chaos
"""

import os
import random
import subprocess
import sys
import time
from datetime import datetime
from pathlib import Path

COMPOSE_FILE = Path(__file__).parent.parent / "system" / "docker-compose.yml"
PROJECT_ROOT = Path(__file__).parent.parent


def parse_services(compose_path: Path) -> list[str]:
    services = []
    in_services = False
    for line in compose_path.read_text().splitlines():
        if line == "services:":
            in_services = True
            continue
        if in_services:
            if line and not line[0].isspace():
                break
            if line.startswith("  ") and not line.startswith("   "):
                name = line.strip().rstrip(":")
                if name:
                    services.append(name)
    return services


def kill_service(service: str) -> None:
    subprocess.run(
        ["docker-compose", "-f", "system/docker-compose.yml", "kill", "-s", "SIGKILL", service],
        cwd=PROJECT_ROOT,
        check=False,
    )


def main() -> None:
    interval = float(os.environ.get("CHAOS_INTERVAL", 15))
    services_per_interval = int(os.environ.get("CHAOS_SERVICES_PER_INTERVAL", 1))
    exclude_csv = os.environ.get("CHAOS_EXCLUDE", "")
    excluded = {s.strip() for s in exclude_csv.split(",") if s.strip()}

    if services_per_interval < 1:
        print("ERROR: CHAOS_SERVICES_PER_INTERVAL must be at least 1.")
        sys.exit(1)

    if not COMPOSE_FILE.exists():
        print(f"ERROR: {COMPOSE_FILE} not found. Run 'make generate-compose' first.")
        sys.exit(1)

    all_services = parse_services(COMPOSE_FILE)
    services = [s for s in all_services if s not in excluded]

    if not services:
        print("ERROR: No services available to kill (check CHAOS_EXCLUDE).")
        sys.exit(1)

    print(f"Chaos mode ON — interval: {interval}s, services per interval: {services_per_interval}")
    print(f"Services pool: {', '.join(services)}")
    if excluded:
        print(f"Excluded: {', '.join(sorted(excluded))}")
    print("Press Ctrl+C to stop.\n")

    try:
        while True:
            targets = random.sample(services, min(services_per_interval, len(services)))
            ts = datetime.now().strftime("%H:%M:%S")
            print(f"[{ts}] Killing: {', '.join(targets)}")
            for target in targets:
                kill_service(target)
            ts = datetime.now().strftime("%H:%M:%S")
            print(f"[{ts}] Done. Next kill round in {interval}s...\n")
            time.sleep(interval)
    except KeyboardInterrupt:
        print("\nChaos stopped.")


if __name__ == "__main__":
    main()
