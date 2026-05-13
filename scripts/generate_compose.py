"""Generate system/docker-compose.yml based on instance counts from .env."""

import os
from pathlib import Path

COMPOSE_OUT = Path(__file__).parent.parent / "system" / "docker-compose.yml"


SERVICES = [
    ("client",  "cmd/client/Dockerfile"),
    ("filter",  "cmd/filter/Dockerfile"),
    ("joiner",  "cmd/joiner/Dockerfile"),
    ("counter", "cmd/counter/Dockerfile"),
]

ENV_VAR = {
    "client":  "N_CLIENTS",
    "filter":  "N_FILTERS",
    "joiner":  "N_JOINERS",
    "counter": "N_COUNTERS",
}


def build_compose(env: dict[str, str]) -> str:
    lines = ["services:"]
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
