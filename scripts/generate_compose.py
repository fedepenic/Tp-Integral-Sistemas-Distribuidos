"""Generate system/docker-compose.yml based on instance counts from .env."""

import os
from pathlib import Path

COMPOSE_OUT = Path(__file__).parent.parent / "system" / "docker-compose.yml"

GATEWAY_PORT = 8080

# Each sink is always a single instance. Add an entry here when a new query
# pipeline is wired end-to-end. (query_id, input_queue, upstream_count_env_var)
SINKS = [
    ("q1", "q1_results", "N_FILTERS"),
]

SERVICES = [
    ("cleaner", "cmd/cleaner/Dockerfile", "N_CLEANERS", {
        "INPUT_QUEUE":      "raw_transactions",
        "OUTPUT_EXCHANGE":  "transactions_clean",
        "OUTPUT_KEYS":      "txn_for_usd,txn_for_q5",
        "RABBITMQ_HOST":    "rabbitmq",
        "RABBITMQ_PORT":    "5672",
        "EOF_EXCHANGE":     "cleaner_eof",
    }),
    ("filter",             "cmd/filter/Dockerfile",             "N_FILTERS",             {}),
    ("joiner",             "cmd/joiner/Dockerfile",             "N_JOINERS",             {}),
    ("counter",            "cmd/counter/Dockerfile",            "N_COUNTERS",            {}),
    ("currency_converter", "cmd/currency_converter/Dockerfile", "N_CURRENCY_CONVERTERS", {
        "INPUT_QUEUE":  "wireach_txn",
        "OUTPUT_QUEUE": "converted_usd",
        "RABBITMQ_HOST": "rabbitmq",
        "RABBITMQ_PORT": "5672",
    }),
]


def build_compose(env: dict[str, str]) -> str:
    lines = ["services:"]

    # RabbitMQ — single instance, must be healthy before gateway starts
    lines.append(f"  rabbitmq:")
    lines.append(f"    build:")
    lines.append(f"      context: .")
    lines.append(f"      dockerfile: cmd/rabbitmq/Dockerfile")
    lines.append(f"    environment:")
    lines.append(f"      - RABBITMQ_LOG_LEVELS=error")
    lines.append(f"    ports:")
    lines.append(f"      - 5672:5672")
    lines.append(f"      - 15672:15672")
    lines.append(f"    healthcheck:")
    lines.append(f"      test: rabbitmq-diagnostics check_port_connectivity")
    lines.append(f"      interval: 5s")
    lines.append(f"      timeout: 3s")
    lines.append(f"      retries: 10")
    lines.append(f"      start_period: 50s")
    lines.append("")

    # Gateway — single instance, no scaling
    lines.append(f"  gateway:")
    lines.append(f"    build:")
    lines.append(f"      context: .")
    lines.append(f"      dockerfile: cmd/gateway/Dockerfile")
    lines.append(f"    environment:")
    lines.append(f"      - GATEWAY_PORT={GATEWAY_PORT}")
    lines.append(f"      - RABBITMQ_HOST=rabbitmq")
    lines.append(f"      - RABBITMQ_PORT=5672")
    lines.append(f"      - OUTPUT_QUEUE=raw_transactions")
    lines.append(f"      - REPORTS_QUEUE=reports")
    lines.append(f"      - OUTPUT_DIR=/output/system")
    lines.append(f"    volumes:")
    lines.append(f"      - ../output:/output")
    lines.append(f"    depends_on:")
    lines.append(f"      rabbitmq:")
    lines.append(f"        condition: service_healthy")
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

    for name, dockerfile, env_var, extra_env in SERVICES:
        count = int(env.get(env_var, 1))
        for i in range(1, count + 1):
            lines.append(f"  {name}_{i}:")
            lines.append(f"    build:")
            lines.append(f"      context: .")
            lines.append(f"      dockerfile: {dockerfile}")
            lines.append(f"    environment:")
            lines.append(f"      - INSTANCE_ID={i}")
            lines.append(f"      - INSTANCE_TOTAL={count}")
            for k, v in extra_env.items():
                lines.append(f"      - {k}={v}")
            lines.append(f"    depends_on:")
            lines.append(f"      rabbitmq:")
            lines.append(f"        condition: service_healthy")
            lines.append("")

    # Sinks — always single-instance, one per query
    for query_id, input_queue, upstream_env_var in SINKS:
        upstream_total = int(env.get(upstream_env_var, 1))
        lines.append(f"  sink_{query_id}:")
        lines.append(f"    build:")
        lines.append(f"      context: .")
        lines.append(f"      dockerfile: cmd/sink/Dockerfile")
        lines.append(f"    environment:")
        lines.append(f"      - QUERY_ID={query_id}")
        lines.append(f"      - INPUT_QUEUE={input_queue}")
        lines.append(f"      - OUTPUT_QUEUE=reports")
        lines.append(f"      - UPSTREAM_TOTAL={upstream_total}")
        lines.append(f"      - RABBITMQ_HOST=rabbitmq")
        lines.append(f"      - RABBITMQ_PORT=5672")
        lines.append(f"    depends_on:")
        lines.append(f"      rabbitmq:")
        lines.append(f"        condition: service_healthy")
        lines.append("")

    return "\n".join(lines) + "\n"


def main():
    env = os.environ
    compose = build_compose(env)
    COMPOSE_OUT.write_text(compose)
    print(f"Written {COMPOSE_OUT}")


if __name__ == "__main__":
    main()
