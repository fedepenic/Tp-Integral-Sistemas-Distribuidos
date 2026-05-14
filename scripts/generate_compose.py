"""Generate system/docker-compose.yml based on instance counts from .env."""

import os
from pathlib import Path

COMPOSE_OUT = Path(__file__).parent.parent / "system" / "docker-compose.yml"

GATEWAY_PORT = 8080

SERVICES = [
    ("filter",  "cmd/filter/Dockerfile"),
    ("joiner",  "cmd/joiner/Dockerfile"),
    ("counter", "cmd/counter/Dockerfile"),
]

ENV_VAR = {
    "filter":  "N_FILTERS",
    "joiner":  "N_JOINERS",
    "counter": "N_COUNTERS",
}


def build_compose(env: dict[str, str]) -> str:
    lines = ["services:"]

    # Gateway — single instance, no scaling
    lines.append(f"  gateway:")
    lines.append(f"    build:")
    lines.append(f"      context: .")
    lines.append(f"      dockerfile: cmd/gateway/Dockerfile")
    lines.append(f"    environment:")
    lines.append(f"      - GATEWAY_PORT={GATEWAY_PORT}")
    lines.append("")

    # Clients
    n_clients = int(env.get("N_CLIENTS", 1))
    batch_size = int(env.get("BATCH_SIZE", 100))
    for i in range(1, n_clients + 1):
        lines.append(f"  client_{i}:")
        lines.append(f"    build:")
        lines.append(f"      context: .")
        lines.append(f"      dockerfile: cmd/client/Dockerfile")
        lines.append(f"    environment:")
        lines.append(f"      - INSTANCE_ID={i}")
        lines.append(f"      - INSTANCE_TOTAL={n_clients}")
        lines.append(f"      - GATEWAY_HOST=gateway")
        lines.append(f"      - GATEWAY_PORT={GATEWAY_PORT}")
        lines.append(f"      - INPUT_DIR=/data/client_{i}")
        lines.append(f"      - BATCH_SIZE={batch_size}")
        lines.append(f"    volumes:")
        lines.append(f"      - ../input:/data")
        lines.append(f"    depends_on:")
        lines.append(f"      - gateway")
        lines.append("")

    # Rest of services
    for name, dockerfile in SERVICES:
        count = int(env.get(ENV_VAR[name], 1))
        for i in range(1, count + 1):
            lines.append(f"  {name}_{i}:")
            lines.append(f"    build:")
            lines.append(f"      context: .")
            lines.append(f"      dockerfile: {dockerfile}")
            lines.append(f"    environment:")
            lines.append(f"      - INSTANCE_ID={i}")
            lines.append(f"      - INSTANCE_TOTAL={count}")
            lines.append("")

    return "\n".join(lines) + "\n"


def main():
    env = os.environ
    compose = build_compose(env)
    COMPOSE_OUT.write_text(compose)
    print(f"Written {COMPOSE_OUT}")


if __name__ == "__main__":
    main()
