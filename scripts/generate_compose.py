"""Generate system/docker-compose.yml based on instance counts from .env."""

import os
from pathlib import Path

COMPOSE_OUT = Path(__file__).parent.parent / "system" / "docker-compose.yml"

GATEWAY_PORT = 8080

# Each sink is always a single instance.
# Data AND EOFs travel through the same input queue (FIFO order).
# (query_id, input_queue, upstream_count_env_var)
SINKS = [
    ("1", "q1_data", "N_AMT50_FILTER"),
    ("2", "q2_data", "N_JOIN_Q2"),
    ("3", "q3_data", "N_AMT_AVG_FILTER"),
    ("5", "q5_count", "N_COUNTER_Q5"),  # N_COUNTER_Q5 defaults to 1
]

# (service_name, input_queue, output_queue, upstream_env_var)
COUNTERS = [
    ("counter_q5", "q5_filtered", "q5_count", "N_USD_LOWER_THAN_ONE"),
]

# (service_name, FILTER_NAME build arg, instance_count_env_var, upstream_count_env_var, extra_env)
# upstream_count_env_var=None means UPSTREAM_INSTANCES is always 1.
NAMED_FILTERS = [
    # usd_filter: coordinated mode (cleaner pattern). Shared input queue +
    # internal EOF broadcast among peer instances. Each instance counts EOFs
    # per client and propagates 1 EOF per client downstream after its own drain.
    ("usd_filter", "usd_filter", "N_USD_FILTER", "N_CLEANERS", {
        "INPUT_QUEUE_NAME":       "usd_filter_input",
        "INPUT_EXCHANGE":         "transactions_clean",
        "INPUT_KEY":              "txn_for_usd",
        "EOF_BROADCAST_EXCHANGE": "usd_filter_eof",
        "OUTPUT_FANOUT_EXCHANGE": "usd_filtered",
        "OUTPUT_DIRECT_EXCHANGE":    "usd_for_q2",
        "OUTPUT_DIRECT_KEY_PREFIX": "maxbank",
    }),
    # amt50_filter: same coordinated pattern. UPSTREAM_INSTANCES=N_USD_FILTER
    # because each upstream usd_filter sends its own EOF per client.
    ("amt50_filter", "lower_than_50_filter", "N_AMT50_FILTER", "N_USD_FILTER", {
        "INPUT_QUEUE_NAME":       "amt50_filter_input",
        "INPUT_EXCHANGE":         "usd_filtered",
        "EOF_BROADCAST_EXCHANGE": "amt50_filter_eof",
        "OUTPUT_QUEUE":           "q1_data",
    }),
    ("period2_filter", "period2_filter", "N_PERIOD2_FILTER", None, {
        "INPUT_QUEUE":     "usd_for_q3p2",
        "OUTPUT_EXCHANGE": "usd_period2",
    }),
    ("period1_filter", "period1_filter", "N_PERIOD1_FILTER", None, {
        "INPUT_QUEUE":           "usd_for_p1",
        "OUTPUT_Q3_EXCHANGE":    "usd_period1_for_q3",
        "OUTPUT_Q4_FO_EXCHANGE": "usd_period1_for_q4_fo",
        "OUTPUT_Q4_FI_EXCHANGE": "usd_period1_for_q4_fi",
    }),
    ("amt_avg_filter", "lower_than_avg_filter", "N_AMT_AVG_FILTER", None, {
        "INPUT_QUEUE":  "q3_candidates",
        "OUTPUT_QUEUE": "q3_data",
    }),
    ("period1_q5_filter", "period1_q5_filter", "N_PERIOD1_Q5_FILTER", "N_CLEANERS", {
        "INPUT_QUEUE_NAME":       "period1_q5_filter_input",
        "INPUT_EXCHANGE":         "transactions_clean",
        "INPUT_KEY":              "txn_for_q5",
        "EOF_BROADCAST_EXCHANGE": "period1_q5_filter_eof",
        "OUTPUT_QUEUE":           "period1_for_q5",
    }),
    ("wireach_filter", "wire_ach_filter", "N_WIREACH_FILTER", "N_PERIOD1_Q5_FILTER", {
        "INPUT_QUEUE":            "period1_for_q5",
        "EOF_BROADCAST_EXCHANGE": "wireach_filter_eof",
        "OUTPUT_QUEUE":           "wireach_txn",
    }),
    ("usd_lower_than_one", "lower_than_1_filter", "N_USD_LOWER_THAN_ONE", "N_CURRENCY_CONVERTERS", {
        "INPUT_QUEUE":            "converted_usd",
        "EOF_BROADCAST_EXCHANGE": "usd_lower_than_one_eof",
        "OUTPUT_QUEUE":           "q5_filtered",
    }),
]

SERVICES = [
    ("cleaner", "cmd/cleaner/Dockerfile", "N_CLEANERS", {
        "INPUT_QUEUE":              "raw_transactions",
        "OUTPUT_EXCHANGE":          "transactions_clean",
        "OUTPUT_KEYS":              "txn_for_usd,txn_for_q5",
        "RABBITMQ_HOST":            "rabbitmq",
        "RABBITMQ_PORT":            "5672",
        "EOF_EXCHANGE":             "cleaner_eof",
        "ACCOUNTS_JOIN_EXCHANGE":   "join_q2_input",
        "ACCOUNTS_JOIN_KEY_PREFIX": "joinq2",
    }),
    # UPSTREAM_INSTANCES = N_WIREACH_FILTER: each wireach_filter instance sends its own EOF.
    ("currency_converter", "cmd/currency_converter/Dockerfile", "N_CURRENCY_CONVERTERS", "N_WIREACH_FILTER", {
        "INPUT_QUEUE":  "wireach_txn",
        "OUTPUT_QUEUE": "converted_usd",
        "RABBITMQ_HOST": "rabbitmq",
        "RABBITMQ_PORT": "5672",
    }),
]

# (service_name, dockerfile, instance_count_env_var, upstream_count_env_var, extra_env)
AGGREGATORS = [
    (
        "max_per_bank",
        "cmd/aggregators/max_per_bank/Dockerfile",
        "N_MAXBANK",
        "N_USD_FILTER",
        {
            "INPUT_EXCHANGE":    "usd_for_q2",
            "INPUT_KEY_PREFIX":  "maxbank",
            "OUTPUT_EXCHANGE":   "join_q2_input",
        }
    ),
    (
        "avg_per_payment_format",
        "cmd/aggregators/avg_per_payment_format/Dockerfile",
        "N_AVG_PER_PAY",
        "N_PERIOD1_FILTER",
        {
            "INPUT_EXCHANGE":       "usd_period1_for_q3",
            "INPUT_KEY_PREFIX":     "avgfmt",
            "OUTPUT_EXCHANGE":      "avg_per_format",
            "EOF_CONTROL_EXCHANGE": "eof_usd_period1_for_q3",
            "EOF_CONTROL_KEY":      "avg_per_payment_format",
        },
    ),
    (
        "fan_in",
        "cmd/aggregators/fan_in/Dockerfile",
        "N_FI",
        "N_PERIOD1_FILTER",
        {
            "INPUT_EXCHANGE":       "usd_period1_for_q4_fi",
            "INPUT_KEY_PREFIX":     "fi",
            "OUTPUT_EXCHANGE":      "scatter_gather_fi",
            "EOF_CONTROL_EXCHANGE": "eof_usd_period1_for_q4_fi",
            "EOF_CONTROL_KEY":      "fan_in",
        },
    ),
    (
        "fan_out",
        "cmd/aggregators/fan_out/Dockerfile",
        "N_FO",
        "N_PERIOD1_FILTER",
        {
            "INPUT_EXCHANGE":       "usd_period1_for_q4_fo",
            "INPUT_KEY_PREFIX":     "fo",
            "OUTPUT_EXCHANGE":      "scatter_gather_fo",
            "EOF_CONTROL_EXCHANGE": "eof_usd_period1_for_q4_fo",
            "EOF_CONTROL_KEY":      "fan_out",
        },
    ),
    (
        "scatter_gather",
        "cmd/aggregators/scatter_gather/Dockerfile",
        "N_SG",
        "N_JOINER_SG",
        {
            "INPUT_EXCHANGE":       "scatter_gather_data",
            "INPUT_KEY":            "scatter_gather",
            "OUTPUT_QUEUE":         "q5_data",
            "EOF_CONTROL_EXCHANGE": "eof_scatter_gather",
            "EOF_CONTROL_KEY":      "scatter_gather",
        },
    ),
]

JOINERS = [
    (
        "join_q2",
        "cmd/joiners/join_q2/Dockerfile",
        "N_JOIN_Q2",
        {
            "INPUT_EXCHANGE":   "join_q2_input",
            "INPUT_KEY_PREFIX": "joinq2",
            "OUTPUT_QUEUE":     "q2_data",
        },
    ),
    (
        "join_q3",
        "cmd/joiners/join_q3/Dockerfile",
        "N_JOIN_Q3",
        {
            "INPUT_QUEUE_NAME_PREFIX": "join_q3_input",
            "INPUT_KEY_PREFIX":        "joinerformat",
            "AVG_INPUT_EXCHANGE":      "avg_per_format",
            "TXN_INPUT_EXCHANGE":      "usd_period2",
            "OUTPUT_EXCHANGE":         "q3_candidates",
            "OUTPUT_KEY":              "",
            "EOF_CONTROL_EXCHANGE":    "eof_join_q3",
            "EOF_CONTROL_KEY_PREFIX":  "join_q3",
        },
    ),
]


def named_filters_extra_env(name: str, env: dict[str, str]) -> dict[str, str]:
    if name == "usd_filter":
        return {
            "OUTPUT_DIRECT_PARTITIONS": env.get("N_MAXBANK", "1"),
        }
    return {}


def services_extra_env(name: str, env: dict[str, str]) -> dict[str, str]:
    if name == "cleaner":
        return {
            "ACCOUNTS_JOIN_PARTITIONS": env.get("N_JOIN_Q2", "1"),
        }
    return {}


def aggregators_extra_env(name: str, env: dict[str, str]) -> dict[str, str]:
    if name == "max_per_bank":
        return {
            "OUTPUT_KEY_PREFIX": env.get("PREFIX_JOIN_Q2", "joinq2"),
            "OUTPUT_PARTITIONS": env.get("N_JOIN_Q2", "1"),
        }
    if name == "avg_per_payment_format":
        return {
            "OUTPUT_KEY_PREFIX": env.get("OUTPUT_KEY_PREFIX", "joinerformat"),
            "OUTPUT_PARTITIONS": env.get("N_JOIN_Q3", "1"),
        }
    if name == "fan_in":
        return {
            "OUTPUT_KEY_PREFIX": env.get("OUTPUT_KEY_PREFIX_FI", "joinersg"),
            "OUTPUT_PARTITIONS": env.get("N_JOINER_SG", "1"),
        }
    if name == "fan_out":
        return {
            "OUTPUT_KEY_PREFIX": env.get("OUTPUT_KEY_PREFIX_FO", "joinersg"),
            "OUTPUT_PARTITIONS": env.get("N_JOINER_SG", "1"),
        }
    return {}


def joiners_extra_env(name: str, env: dict[str, str]) -> dict[str, str]:
    if name == "join_q2":
        return {
            "ACCOUNTS_UPSTREAM_INSTANCES":  env.get("N_CLEANERS", "1"),
            "MAX_PER_BANK_UPSTREAM_INSTANCES": env.get("N_MAXBANK", "1"),
        }
    if name == "join_q3":
        return {
            "AVG_UPSTREAM_INSTANCES": env.get("N_AVG_PER_PAY", "1"),
            "TXN_UPSTREAM_INSTANCES": env.get("N_PERIOD2_FILTER", "1"),
        }
    return {}


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
    batch_size = int(env.get("BATCH_SIZE", 1000))
    transactions_file = env.get("TRANSACTIONS_FILE", "LI-Medium_Trans.csv")
    accounts_file = env.get("ACCOUNTS_FILE", "LI-Medium_accounts.csv")
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
        lines.append(f"      - TRANSACTIONS_FILE={transactions_file}")
        lines.append(f"      - ACCOUNTS_FILE={accounts_file}")
        lines.append(f"    volumes:")
        lines.append(f"      - ../input:/data")
        lines.append(f"    depends_on:")
        lines.append(f"      - gateway")
        lines.append("")

    for entry in SERVICES:
        name, dockerfile, env_var = entry[0], entry[1], entry[2]
        upstream_env_var = entry[3] if len(entry) > 4 else None
        extra_env = entry[4] if len(entry) > 4 else entry[3]
        count = int(env.get(env_var, 1))
        upstream = int(env.get(upstream_env_var, 1)) if upstream_env_var else None
        for i in range(1, count + 1):
            lines.append(f"  {name}_{i}:")
            lines.append(f"    build:")
            lines.append(f"      context: .")
            lines.append(f"      dockerfile: {dockerfile}")
            lines.append(f"    environment:")
            lines.append(f"      - INSTANCE_ID={i}")
            lines.append(f"      - INSTANCE_TOTAL={count}")
            if upstream is not None:
                lines.append(f"      - UPSTREAM_INSTANCES={upstream}")
            env_map = dict(extra_env)
            env_map.update(services_extra_env(name, env))
            for k, v in env_map.items():
                lines.append(f"      - {k}={v}")
            lines.append(f"    depends_on:")
            lines.append(f"      rabbitmq:")
            lines.append(f"        condition: service_healthy")
            lines.append("")

    # Aggregators
    for name, dockerfile, count_env_var, upstream_env_var, extra_env in AGGREGATORS:
        count = int(env.get(count_env_var, 1))
        upstream = int(env.get(upstream_env_var, 1)) if upstream_env_var else 1
        env_map = dict(extra_env)
        env_map.update(aggregators_extra_env(name, env))
        input_prefix = extra_env.get("INPUT_KEY_PREFIX", "")
        for i in range(1, count + 1):
            lines.append(f"  {name}_{i}:")
            lines.append(f"    build:")
            lines.append(f"      context: .")
            lines.append(f"      dockerfile: {dockerfile}")
            lines.append(f"    environment:")
            lines.append(f"      - INSTANCE_ID={i}")
            lines.append(f"      - INSTANCE_TOTAL={count}")
            lines.append(f"      - RABBITMQ_HOST=rabbitmq")
            lines.append(f"      - RABBITMQ_PORT=5672")
            lines.append(f"      - UPSTREAM_INSTANCES={upstream}")
            if input_prefix:
                lines.append(f"      - INPUT_KEY={input_prefix}_{i-1}")
            for k, v in env_map.items():
                if k == "INPUT_KEY_PREFIX":
                    continue
                lines.append(f"      - {k}={v}")
            lines.append(f"    depends_on:")
            lines.append(f"      rabbitmq:")
            lines.append(f"        condition: service_healthy")
            lines.append("")

    # Joiners
    for name, dockerfile, count_env_var, extra_env in JOINERS:
        count = int(env.get(count_env_var, 1))
        env_map = dict(extra_env)
        env_map.update(joiners_extra_env(name, env))
        input_prefix = extra_env.get("INPUT_KEY_PREFIX", "")
        input_queue_prefix = extra_env.get("INPUT_QUEUE_NAME_PREFIX", "")
        eof_prefix = extra_env.get("EOF_CONTROL_KEY_PREFIX", "")
        for i in range(1, count + 1):
            lines.append(f"  {name}_{i}:")
            lines.append(f"    build:")
            lines.append(f"      context: .")
            lines.append(f"      dockerfile: {dockerfile}")
            lines.append(f"    environment:")
            lines.append(f"      - INSTANCE_ID={i}")
            lines.append(f"      - INSTANCE_TOTAL={count}")
            lines.append(f"      - RABBITMQ_HOST=rabbitmq")
            lines.append(f"      - RABBITMQ_PORT=5672")
            if input_prefix:
                lines.append(f"      - INPUT_KEY={input_prefix}_{i-1}")
            if input_queue_prefix:
                lines.append(f"      - INPUT_QUEUE_NAME={input_queue_prefix}_{i-1}")
            if eof_prefix:
                lines.append(f"      - EOF_CONTROL_KEY={eof_prefix}_{i-1}")
            for k, v in env_map.items():
                if k in ("INPUT_KEY_PREFIX", "INPUT_QUEUE_NAME_PREFIX", "EOF_CONTROL_KEY_PREFIX"):
                    continue
                lines.append(f"      - {k}={v}")
            lines.append(f"    depends_on:")
            lines.append(f"      rabbitmq:")
            lines.append(f"        condition: service_healthy")
            lines.append("")

    # Counters — single instance, aggregate transactions into a count before the sink
    for svc_name, input_queue, output_queue, upstream_env_var in COUNTERS:
        upstream = int(env.get(upstream_env_var, 1))
        lines.append(f"  {svc_name}:")
        lines.append(f"    build:")
        lines.append(f"      context: .")
        lines.append(f"      dockerfile: cmd/counter/Dockerfile")
        lines.append(f"    environment:")
        lines.append(f"      - INPUT_QUEUE={input_queue}")
        lines.append(f"      - OUTPUT_QUEUE={output_queue}")
        lines.append(f"      - UPSTREAM_INSTANCES={upstream}")
        lines.append(f"      - RABBITMQ_HOST=rabbitmq")
        lines.append(f"      - RABBITMQ_PORT=5672")
        lines.append(f"    depends_on:")
        lines.append(f"      rabbitmq:")
        lines.append(f"        condition: service_healthy")
        lines.append("")

    # Sinks — always single-instance, one per query
    for query_id, input_queue, upstream_env_var in SINKS:
        upstream = int(env.get(upstream_env_var, 1))
        lines.append(f"  sink_{query_id}:")
        lines.append(f"    build:")
        lines.append(f"      context: .")
        lines.append(f"      dockerfile: cmd/sink/Dockerfile")
        lines.append(f"    environment:")
        lines.append(f"      - QUERY_ID={query_id}")
        lines.append(f"      - INPUT_QUEUE={input_queue}")
        lines.append(f"      - OUTPUT_QUEUE=reports")
        lines.append(f"      - UPSTREAM_INSTANCES={upstream}")
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
            nf_env = dict(extra_env)
            nf_env.update(named_filters_extra_env(svc_name, env))
            for k, v in nf_env.items():
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
