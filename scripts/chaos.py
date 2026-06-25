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
import re
import shutil
import subprocess
import sys
import time
from pathlib import Path

COMPOSE_FILE = Path(__file__).parent.parent / "system" / "docker-compose.yml"
PROJECT_ROOT = Path(__file__).parent.parent
INSTANCE_SUFFIX_RE = re.compile(r"^(?P<base>.+)_(?P<instance>\d+)$")
KILL_ALL_SERVICE_KINDS = {"client", "gateway"}


def docker_compose_cmd() -> list[str]:
    if shutil.which("docker-compose"):
        return ["docker-compose"]

    return ["docker", "compose"]


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


def service_kind(service: str) -> str:
    match = INSTANCE_SUFFIX_RE.match(service)
    if match:
        return match.group("base")
    return service


def is_excluded(service: str, excluded: set[str]) -> bool:
    return service in excluded or service_kind(service) in excluded


def group_services(services: list[str]) -> dict[str, list[str]]:
    groups: dict[str, list[str]] = {}
    for service in services:
        groups.setdefault(service_kind(service), []).append(service)
    return groups


def can_kill_all_instances(service: str) -> bool:
    return service_kind(service) in KILL_ALL_SERVICE_KINDS or service.startswith("sink_")


def running_services(services: list[str]) -> set[str]:
    running = set()
    for service in services:
        ps = subprocess.run(
            [*docker_compose_cmd(), "-f", "system/docker-compose.yml", "ps", "-q", service],
            cwd=PROJECT_ROOT,
            check=False,
            capture_output=True,
            text=True,
        )
        container_ids = ps.stdout.split()
        if ps.returncode != 0 or not container_ids:
            continue

        inspect = subprocess.run(
            ["docker", "inspect", "-f", "{{.State.Running}}", *container_ids],
            cwd=PROJECT_ROOT,
            check=False,
            capture_output=True,
            text=True,
        )
        if inspect.returncode == 0 and "true" in inspect.stdout.split():
            running.add(service)
    return running


def choose_targets(services_by_kind: dict[str, list[str]], services_per_interval: int) -> list[str]:
    running = running_services([svc for services in services_by_kind.values() for svc in services])
    candidates = []

    for services in services_by_kind.values():
        running_group = [svc for svc in services if svc in running]
        if not running_group:
            continue
        if all(can_kill_all_instances(svc) for svc in running_group):
            candidates.extend(running_group)
        elif len(running_group) > 1:
            candidates.extend(random.sample(running_group, len(running_group) - 1))

    if not candidates:
        return []

    return random.sample(candidates, min(services_per_interval, len(candidates)))


def kill_service(service: str) -> None:
    subprocess.run(
        [*docker_compose_cmd(), "-f", "system/docker-compose.yml", "kill", "-s", "SIGKILL", service],
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
    services = [s for s in all_services if not is_excluded(s, excluded)]
    services_by_kind = group_services(services)
    killable_services = [
        service
        for services_group in services_by_kind.values()
        if len(services_group) > 1 or all(can_kill_all_instances(s) for s in services_group)
        for service in services_group
    ]

    if not killable_services:
        print("ERROR: No services available to kill (check CHAOS_EXCLUDE).")
        sys.exit(1)

    print(f"Chaos mode ON — interval: {interval}s, services per interval: {services_per_interval}")
    print(f"Services pool: {', '.join(killable_services)}")
    if excluded:
        print(f"Excluded: {', '.join(sorted(excluded))}")
    protected = sorted(set(services) - set(killable_services))
    if protected:
        print(f"Protected singletons: {', '.join(protected)}")
    print("Press Ctrl+C to stop.\n")

    try:
        while True:
            targets = choose_targets(services_by_kind, services_per_interval)
            if not targets:
                print(f"No safe targets available. Waiting {interval}s...\n")
                time.sleep(interval)
                continue
            print(f"Killing: {', '.join(targets)}")
            for target in targets:
                kill_service(target)
            print(f"Done. Next kill round in {interval}s...\n")
            time.sleep(interval)
    except KeyboardInterrupt:
        print("\nChaos stopped.")


if __name__ == "__main__":
    main()
