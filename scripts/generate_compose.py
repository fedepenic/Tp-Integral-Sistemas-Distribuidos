"""Generate system/docker-compose.yml based on instance counts from .env."""

import os
from pathlib import Path

COMPOSE_OUT = Path(__file__).parent.parent / "system" / "docker-compose.yml"

GATEWAY_PORT = 8080

# Each sink is always a single instance with a single upstream aggregator.
# (query_id, input_queue)
SINKS = [
    ("1", "q1_data"),
]

# (service_name, FILTER_NAME build arg, instance_count_env_var, upstream_count_env_var, extra_env)
# upstream_count_env_var=None means UPSTREAM_INSTANCES is always 1.
NAMED_FILTERS = [
    ("usd_filter", "usd_filter", "N_USD_FILTER", "N_CLEANERS", {
        "INPUT_EXCHANGE":         "transactions_clean",
        "INPUT_KEY":              "txn_for_usd",
        "OUTPUT_FANOUT_EXCHANGE": "usd_filtered",
        "OUTPUT_DIRECT_EXCHANGE": "usd_for_q2",
        "EOF_INPUT_EXCHANGE":     "eof_cleaner",
        "EOF_INPUT_KEY":          "usd_filter",
        "EOF_FANOUT_EXCHANGE":    "eof_usd_filtered",
        "EOF_DIRECT_EXCHANGE":    "eof_usd_for_q2",
    }),
    ("amt50_filter", "lower_than_50_filter", "N_AMT50_FILTER", None, {
        "INPUT_EXCHANGE":      "usd_filtered",
        "OUTPUT_QUEUE":        "q1_data",
        "EOF_INPUT_EXCHANGE":  "eof_usd_filtered",
        "EOF_INPUT_KEY":       "amt50_filter",
        "EOF_OUTPUT_EXCHANGE": "eof_q1_data",
    }),
    ("period2_filter", "period2_filter", "N_PERIOD2_FILTER", None, {
        "INPUT_QUEUE":         "usd_for_q3p2",
        "OUTPUT_QUEUE":        "usd_period2",
        "EOF_INPUT_EXCHANGE":  "eof_usd_filtered",
        "EOF_INPUT_KEY":       "period2_filter",
        "EOF_OUTPUT_EXCHANGE": "eof_usd_period2",
    }),
    ("period1_filter", "period1_filter", "N_PERIOD1_FILTER", None, {
        "INPUT_QUEUE":           "usd_for_p1",
        "OUTPUT_Q3_EXCHANGE":    "usd_period1_for_q3",
        "OUTPUT_Q4_FO_EXCHANGE": "usd_period1_for_q4_fo",
        "OUTPUT_Q4_FI_EXCHANGE": "usd_period1_for_q4_fi",
        "EOF_INPUT_EXCHANGE":    "eof_usd_filtered",
        "EOF_INPUT_KEY":         "period1_filter",
        "EOF_Q3_EXCHANGE":       "eof_usd_period1_for_q3",
        "EOF_Q4_FO_EXCHANGE":    "eof_usd_period1_for_q4_fo",
        "EOF_Q4_FI_EXCHANGE":    "eof_usd_period1_for_q4_fi",
    }),
    ("amt_avg_filter", "lower_than_avg_filter", "N_AMT_AVG_FILTER", None, {
        "INPUT_QUEUE":         "q3_candidates",
        "OUTPUT_QUEUE":        "q3_data",
        "EOF_INPUT_EXCHANGE":  "eof_q3_candidates",
        "EOF_INPUT_KEY":       "amt_avg_filter",
        "EOF_OUTPUT_EXCHANGE": "eof_q3_data",
    }),
    ("period1_q5_filter", "period1_q5_filter", "N_PERIOD1_Q5_FILTER", "N_CLEANERS", {
        "INPUT_EXCHANGE":      "transactions_clean",
        "INPUT_KEY":           "txn_for_q5",
        "OUTPUT_QUEUE":        "period1_for_q5",
        "EOF_INPUT_EXCHANGE":  "eof_cleaner",
        "EOF_INPUT_KEY":       "period1_q5_filter",
        "EOF_OUTPUT_EXCHANGE": "eof_period1_for_q5",
    }),
    ("wireach_filter", "wire_ach_filter", "N_WIREACH_FILTER", None, {
        "INPUT_QUEUE":         "period1_for_q5",
        "OUTPUT_QUEUE":        "wireach_txn",
        "EOF_INPUT_EXCHANGE":  "eof_period1_for_q5",
        "EOF_INPUT_KEY":       "wireach_filter",
        "EOF_OUTPUT_EXCHANGE": "eof_wireach_txn",
    }),
    ("usd_lower_than_one", "lower_than_1_filter", "N_USD_LOWER_THAN_ONE", None, {
        "INPUT_QUEUE":         "converted_usd",
        "OUTPUT_QUEUE":        "q5_filtered",
        "EOF_INPUT_EXCHANGE":  "eof_converted_usd",
        "EOF_INPUT_KEY":       "usd_lower_than_one",
        "EOF_OUTPUT_EXCHANGE": "eof_q5_filtered",
    }),
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
    for query_id, input_queue in SINKS:
        lines.append(f"  sink_{query_id}:")
        lines.append(f"    build:")
        lines.append(f"      context: .")
        lines.append(f"      dockerfile: cmd/sink/Dockerfile")
        lines.append(f"    environment:")
        lines.append(f"      - QUERY_ID={query_id}")
        lines.append(f"      - INPUT_QUEUE={input_queue}")
        lines.append(f"      - OUTPUT_QUEUE=reports")
        lines.append(f"      - UPSTREAM_TOTAL=1")
        lines.append(f"      - RABBITMQ_HOST=rabbitmq")
        lines.append(f"      - RABBITMQ_PORT=5672")
        lines.append(f"    depends_on:")
        lines.append(f"      rabbitmq:")
        lines.append(f"        condition: service_healthy")
        lines.append("")

    # Named filters — instance count driven by .env, with specific build args and env vars
    for svc_name, filter_name, count_env_var, upstream_env_var, extra_env in NAMED_FILTERS:
        count = int(env.get(count_env_var, 1))
        upstream = int(env.get(upstream_env_var, 1)) if upstream_env_var else 1
        for i in range(1, count + 1):
            lines.append(f"  {svc_name}_{i}:")
            lines.append(f"    build:")
            lines.append(f"      context: .")
            lines.append(f"      dockerfile: cmd/filter/Dockerfile")
            lines.append(f"      args:")
            lines.append(f"        FILTER_NAME: {filter_name}")
            lines.append(f"    environment:")
            lines.append(f"      - INSTANCE_ID={i}")
            lines.append(f"      - INSTANCE_TOTAL={count}")
            lines.append(f"      - RABBITMQ_HOST=rabbitmq")
            lines.append(f"      - RABBITMQ_PORT=5672")
            lines.append(f"      - UPSTREAM_INSTANCES={upstream}")
            for k, v in extra_env.items():
                lines.append(f"      - {k}={v}")
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
