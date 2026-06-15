"""Generate system/docker-compose.yml based on instance counts from .env.

Set TEST_QUERIES=1,3 (comma-separated) to include only the nodes required
for those queries. Omit it to include every query (default).
"""

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
    ("4", "q5_data",  "N_SG"),
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
    ("period2_filter", "period2_filter", "N_PERIOD2_FILTER", "N_USD_FILTER", {
        "INPUT_QUEUE_NAME":   "period2_filter_input",
        "INPUT_EXCHANGE":     "usd_filtered",
        "EOF_BROADCAST_EXCHANGE": "period2_filter_eof",
        "OUTPUT_EXCHANGE":    "usd_period2",
        "OUTPUT_KEY_PREFIX":  "joinerformat",
    }),
    ("period1_filter", "period1_filter", "N_PERIOD1_FILTER", "N_USD_FILTER", {
        "INPUT_QUEUE_NAME":       "period1_filter_input",
        "INPUT_EXCHANGE":         "usd_filtered",
        "EOF_BROADCAST_EXCHANGE": "period1_filter_eof",
        "OUTPUT_Q3_EXCHANGE":     "usd_period1_for_q3",
        "OUTPUT_Q3_KEY_PREFIX":   "avgfmt",
        "OUTPUT_Q4_EXCHANGE":     "usd_period1_for_q4",
        "OUTPUT_Q4_KEY_PREFIX":   "q4sf",
    }),
    ("amt_avg_filter", "lower_than_avg_filter", "N_AMT_AVG_FILTER", "N_JOIN_Q3", {
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
    ("cleaner", "cleaner", "N_CLEANERS", {
        "INPUT_QUEUE":              "raw_transactions",
        "OUTPUT_EXCHANGE":          "transactions_clean",
        "RABBITMQ_HOST":            "rabbitmq",
        "RABBITMQ_PORT":            "5672",
        "EOF_EXCHANGE":             "cleaner_eof",
        "ACCOUNTS_JOIN_EXCHANGE":   "join_q2_input",
        "ACCOUNTS_JOIN_KEY_PREFIX": "joinq2",
    }),
    # UPSTREAM_INSTANCES = N_WIREACH_FILTER: each wireach_filter instance sends its own EOF.
    ("currency_converter", "currency_converter", "N_CURRENCY_CONVERTERS", "N_WIREACH_FILTER", {
        "INPUT_QUEUE":  "wireach_txn",
        "OUTPUT_QUEUE": "converted_usd",
        "RABBITMQ_HOST": "rabbitmq",
        "RABBITMQ_PORT": "5672",
    }),
]

# (service_name, service_path, instance_count_env_var, upstream_count_env_var, extra_env)
AGGREGATORS = [
    (
        "max_per_bank",
        "aggregators/max_per_bank",
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
        "aggregators/avg_per_payment_format",
        "N_AVG_PER_PAY",
        "N_PERIOD1_FILTER",
        {
            "INPUT_EXCHANGE":   "usd_period1_for_q3",
            "INPUT_KEY_PREFIX": "avgfmt",
            "OUTPUT_EXCHANGE":  "avg_per_format",
        },
    ),
    (
        "fan_src_filter",
        "aggregators/fan_src_filter",
        "N_SRC_FILTER",
        "N_PERIOD1_FILTER",
        {
            "INPUT_EXCHANGE":   "usd_period1_for_q4",
            "INPUT_KEY_PREFIX": "q4sf",
        },
    ),
    (
        "fan_in",
        "aggregators/fan_in",
        "N_FI",
        "N_SRC_FILTER",
        {
            "INPUT_EXCHANGE":   "usd_period1_for_q4_fi",
            "INPUT_KEY_PREFIX": "fi",
            "OUTPUT_EXCHANGE":  "scatter_gather_fi",
        },
    ),
    (
        "fan_out",
        "aggregators/fan_out",
        "N_FO",
        "N_SRC_FILTER",
        {
            "INPUT_EXCHANGE":   "usd_period1_for_q4_fo",
            "INPUT_KEY_PREFIX": "fo",
            "OUTPUT_EXCHANGE":  "scatter_gather_fo",
        },
    ),
    (
        "scatter_gather",
        "aggregators/scatter_gather",
        "N_SG",
        "N_JOINER_SG",
        {
            "INPUT_EXCHANGE":   "scatter_gather_data",
            "INPUT_KEY_PREFIX": "sg",
            "OUTPUT_QUEUE":     "q5_data",
        },
    ),
]

JOINERS = [
    (
        "join_q2",
        "joiners/join_q2",
        "N_JOIN_Q2",
        {
            "INPUT_EXCHANGE":   "join_q2_input",
            "INPUT_KEY_PREFIX": "joinq2",
            "OUTPUT_QUEUE":     "q2_data",
        },
    ),
    (
        "join_q3",
        "joiners/join_q3",
        "N_JOIN_Q3",
        {
            "INPUT_QUEUE_NAME_PREFIX": "join_q3_input",
            "INPUT_KEY_PREFIX":        "joinerformat",
            "AVG_INPUT_EXCHANGE":      "avg_per_format",
            "TXN_INPUT_EXCHANGE":      "usd_period2",
            "OUTPUT_QUEUE":            "q3_candidates",
        },
    ),
    (
        "joiner_sg",
        "joiners/joiner_sg",
        "N_JOINER_SG",
        {
            "INPUT_QUEUE_NAME_PREFIX": "joiner_sg_input",
            "INPUT_KEY_PREFIX":        "joinersg",
            "FO_INPUT_EXCHANGE":       "scatter_gather_fo",
            "FI_INPUT_EXCHANGE":       "scatter_gather_fi",
            "OUTPUT_EXCHANGE":         "scatter_gather_data",
            "OUTPUT_KEY_PREFIX":       "sg",
        },
    ),
]

# ---------------------------------------------------------------------------
# Query membership: which queries each service is required for.
# Services absent from these dicts are always included (cleaner, gateway, etc.)
# ---------------------------------------------------------------------------

ALL_QUERIES = {"1", "2", "3", "4", "5"}

NAMED_FILTER_QUERIES: dict[str, set[str]] = {
    "usd_filter":         {"1", "2", "3", "4"},
    "amt50_filter":       {"1"},
    "period2_filter":     {"3"},
    "period1_filter":     {"3", "4"},
    "amt_avg_filter":     {"3"},
    "period1_q5_filter":  {"5"},
    "wireach_filter":     {"5"},
    "usd_lower_than_one": {"5"},
}

AGGREGATOR_QUERIES: dict[str, set[str]] = {
    "max_per_bank":           {"2"},
    "avg_per_payment_format": {"3"},
    "fan_src_filter":         {"4"},
    "fan_in":                 {"4"},
    "fan_out":                {"4"},
    "scatter_gather":         {"4"},
}

JOINER_QUERIES: dict[str, set[str]] = {
    "join_q2":   {"2"},
    "join_q3":   {"3"},
    "joiner_sg": {"4"},
}

SERVICE_QUERIES: dict[str, set[str]] = {
    # cleaner is always included — its OUTPUT_KEYS are computed dynamically
    "currency_converter": {"5"},
}

COUNTER_QUERIES: dict[str, set[str]] = {
    "counter_q5": {"5"},
}

SINK_QUERIES: dict[str, set[str]] = {
    "1": {"1"},
    "2": {"2"},
    "3": {"3"},
    "4": {"4"},
    "5": {"5"},
}


def get_active_queries(env: dict[str, str]) -> set[str]:
    """Return the set of query IDs to include. Defaults to all queries."""
    raw = env.get("TEST_QUERIES", "").strip()
    if not raw:
        return ALL_QUERIES
    return {q.strip() for q in raw.split(",") if q.strip()}


def cleaner_output_keys(active_queries: set[str]) -> str:
    keys = []
    if active_queries & {"1", "2", "3", "4"}:
        keys.append("txn_for_usd")
    if "5" in active_queries:
        keys.append("txn_for_q5")
    return ",".join(keys)


def get_instance_count(env: dict[str, str], env_var: str, default: int = 1) -> int:
    """Get instance count from env, respecting N_WORKERS global override.

    If N_WORKERS > 0, uses that value for all scalable nodes.
    Otherwise, uses the individual env_var value.
    """
    n_workers = int(env.get("N_WORKERS", 0))
    if n_workers > 0:
        return n_workers
    return int(env.get(env_var, default))


def named_filters_extra_env(name: str, env: dict[str, str]) -> dict[str, str]:
    if name == "usd_filter":
        return {
            "OUTPUT_DIRECT_PARTITIONS": env.get("N_MAXBANK", "1"),
        }
    if name == "period2_filter":
        return {
            "OUTPUT_PARTITIONS": env.get("N_JOIN_Q3", "1"),
        }
    if name == "period1_filter":
        return {
            "OUTPUT_Q3_PARTITIONS": env.get("N_AVG_PER_PAY", "1"),
            "OUTPUT_Q4_PARTITIONS": env.get("N_SRC_FILTER", "1"),
        }
    return {}


def services_extra_env(name: str, env: dict[str, str], active_queries: set[str]) -> dict[str, str]:
    if name == "cleaner":
        return {
            "OUTPUT_KEYS":              cleaner_output_keys(active_queries),
            "ACCOUNTS_JOIN_PARTITIONS": env.get("N_JOIN_Q2", "1"),
            "SKIP_CLEANING":            env.get("SKIP_CLEANING", "false"),
        }
    if name == "currency_converter":
        return {
            "USE_HARDCODED_RATES": env.get("USE_HARDCODED_RATES", "true"),
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
    if name == "fan_src_filter":
        return {
            "OUTPUT_FO_EXCHANGE":   "usd_period1_for_q4_fo",
            "OUTPUT_FO_KEY_PREFIX": "fo",
            "OUTPUT_FO_PARTITIONS": env.get("N_FO", "1"),
            "OUTPUT_FI_EXCHANGE":   "usd_period1_for_q4_fi",
            "OUTPUT_FI_KEY_PREFIX": "fi",
            "OUTPUT_FI_PARTITIONS": env.get("N_FI", "1"),
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
    if name == "joiner_sg":
        return {
            "FO_UPSTREAM_INSTANCES": env.get("N_FO", "1"),
            "FI_UPSTREAM_INSTANCES": env.get("N_FI", "1"),
            "OUTPUT_PARTITIONS":     env.get("N_SG", "1"),
        }
    return {}


def active_for(name: str, membership: dict[str, set[str]], active_queries: set[str]) -> bool:
    """Return True if the service should be included given the active query set."""
    return bool(membership.get(name, ALL_QUERIES) & active_queries)


HEALTH_PORT = 9999


def get_bool_env(env: dict[str, str], key: str, default: bool = False) -> bool:
    value = env.get(key)
    if value is None or value.strip() == "":
        return default
    return value.strip().lower() in ("1", "true", "yes", "on")


def append_watcher_health_env(lines: list[str], watcher_enabled: bool) -> None:
    if watcher_enabled:
        lines.append(f"      - ENABLE_WATCHER=true")
        lines.append(f"      - HEALTH_PORT={HEALTH_PORT}")


def append_watcher_dependency(lines: list[str], watcher_enabled: bool) -> None:
    if watcher_enabled:
        lines.append(f"      watcher:")
        lines.append(f"        condition: service_started")


def build_compose(env: dict[str, str], active_queries: set[str]) -> str:
    lines = ["services:"]
    watcher_enabled = get_bool_env(env, "ENABLE_WATCHER", True)
    # Collect (service_name, port) for every long-running service so the watcher
    # can be configured with the full list at the end of this function.
    watcher_targets: list[tuple[str, int]] = [
        ("rabbitmq", 5672),
    ]

    # RabbitMQ — single instance, must be healthy before gateway starts
    lines.append(f"  rabbitmq:")
    lines.append(f"    image: rabbitmq")
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
    lines.append(f"    image: gateway")
    lines.append(f"    build:")
    lines.append(f"      context: .")
    lines.append(f"      dockerfile: cmd/Dockerfile")
    lines.append(f"      args:")
    lines.append(f"        SERVICE_PATH: gateway")
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
    n_clients = get_instance_count(env, "N_CLIENTS", 1)
    batch_size = int(env.get("BATCH_SIZE", 1000))
    transactions_file = env.get("TRANSACTIONS_FILE", "LI-Medium_Trans.csv")
    accounts_file = env.get("ACCOUNTS_FILE", "LI-Medium_accounts.csv")
    for i in range(1, n_clients + 1):
        lines.append(f"  client_{i}:")
        lines.append(f"    image: client_{i}")
        lines.append(f"    build:")
        lines.append(f"      context: .")
        lines.append(f"      dockerfile: cmd/Dockerfile")
        lines.append(f"      args:")
        lines.append(f"        SERVICE_PATH: client")
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
        name, service_path, env_var = entry[0], entry[1], entry[2]
        upstream_env_var = entry[3] if len(entry) > 4 else None
        extra_env = entry[4] if len(entry) > 4 else entry[3]
        if not active_for(name, SERVICE_QUERIES, active_queries):
            continue
        count = get_instance_count(env, env_var, 1)
        upstream = get_instance_count(env, upstream_env_var, 1) if upstream_env_var else None
        for i in range(1, count + 1):
            svc_instance = f"{name}_{i}"
            watcher_targets.append((svc_instance, HEALTH_PORT))
            lines.append(f"  {svc_instance}:")
            lines.append(f"    image: {svc_instance}")
            lines.append(f"    build:")
            lines.append(f"      context: .")
            lines.append(f"      dockerfile: cmd/Dockerfile")
            lines.append(f"      args:")
            lines.append(f"        SERVICE_PATH: {service_path}")
            lines.append(f"    environment:")
            lines.append(f"      - INSTANCE_ID={i}")
            lines.append(f"      - INSTANCE_TOTAL={count}")
            if upstream is not None:
                lines.append(f"      - UPSTREAM_INSTANCES={upstream}")
            env_map = dict(extra_env)
            env_map.update(services_extra_env(name, env, active_queries))
            for k, v in env_map.items():
                lines.append(f"      - {k}={v}")
            append_watcher_health_env(lines, watcher_enabled)
            lines.append(f"    depends_on:")
            lines.append(f"      rabbitmq:")
            lines.append(f"        condition: service_healthy")
            append_watcher_dependency(lines, watcher_enabled)
            lines.append("")

    # Aggregators
    for name, service_path, count_env_var, upstream_env_var, extra_env in AGGREGATORS:
        if not active_for(name, AGGREGATOR_QUERIES, active_queries):
            continue
        count = get_instance_count(env, count_env_var, 1)
        upstream = get_instance_count(env, upstream_env_var, 1) if upstream_env_var else 1
        env_map = dict(extra_env)
        env_map.update(aggregators_extra_env(name, env))
        input_prefix = extra_env.get("INPUT_KEY_PREFIX", "")
        for i in range(1, count + 1):
            svc_instance = f"{name}_{i}"
            watcher_targets.append((svc_instance, HEALTH_PORT))
            lines.append(f"  {svc_instance}:")
            lines.append(f"    image: {svc_instance}")
            lines.append(f"    build:")
            lines.append(f"      context: .")
            lines.append(f"      dockerfile: cmd/Dockerfile")
            lines.append(f"      args:")
            lines.append(f"        SERVICE_PATH: {service_path}")
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
            append_watcher_health_env(lines, watcher_enabled)
            lines.append(f"    depends_on:")
            lines.append(f"      rabbitmq:")
            lines.append(f"        condition: service_healthy")
            append_watcher_dependency(lines, watcher_enabled)
            lines.append("")

    # Joiners
    for name, service_path, count_env_var, extra_env in JOINERS:
        if not active_for(name, JOINER_QUERIES, active_queries):
            continue
        count = get_instance_count(env, count_env_var, 1)
        env_map = dict(extra_env)
        env_map.update(joiners_extra_env(name, env))
        input_prefix = extra_env.get("INPUT_KEY_PREFIX", "")
        input_queue_prefix = extra_env.get("INPUT_QUEUE_NAME_PREFIX", "")
        eof_prefix = extra_env.get("EOF_CONTROL_KEY_PREFIX", "")
        for i in range(1, count + 1):
            svc_instance = f"{name}_{i}"
            watcher_targets.append((svc_instance, HEALTH_PORT))
            lines.append(f"  {svc_instance}:")
            lines.append(f"    image: {svc_instance}")
            lines.append(f"    build:")
            lines.append(f"      context: .")
            lines.append(f"      dockerfile: cmd/Dockerfile")
            lines.append(f"      args:")
            lines.append(f"        SERVICE_PATH: {service_path}")
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
            append_watcher_health_env(lines, watcher_enabled)
            lines.append(f"    depends_on:")
            lines.append(f"      rabbitmq:")
            lines.append(f"        condition: service_healthy")
            append_watcher_dependency(lines, watcher_enabled)
            lines.append("")

    # Counters — single instance, aggregate transactions into a count before the sink
    for svc_name, input_queue, output_queue, upstream_env_var in COUNTERS:
        if not active_for(svc_name, COUNTER_QUERIES, active_queries):
            continue
        upstream = get_instance_count(env, upstream_env_var, 1)
        watcher_targets.append((svc_name, HEALTH_PORT))
        lines.append(f"  {svc_name}:")
        lines.append(f"    image: {svc_name}")
        lines.append(f"    build:")
        lines.append(f"      context: .")
        lines.append(f"      dockerfile: cmd/Dockerfile")
        lines.append(f"      args:")
        lines.append(f"        SERVICE_PATH: counter")
        lines.append(f"    environment:")
        lines.append(f"      - INPUT_QUEUE={input_queue}")
        lines.append(f"      - OUTPUT_QUEUE={output_queue}")
        lines.append(f"      - UPSTREAM_INSTANCES={upstream}")
        lines.append(f"      - RABBITMQ_HOST=rabbitmq")
        lines.append(f"      - RABBITMQ_PORT=5672")
        append_watcher_health_env(lines, watcher_enabled)
        lines.append(f"    depends_on:")
        lines.append(f"      rabbitmq:")
        lines.append(f"        condition: service_healthy")
        append_watcher_dependency(lines, watcher_enabled)
        lines.append("")

    # Sinks — always single-instance, one per query
    for query_id, input_queue, upstream_env_var in SINKS:
        if not active_for(query_id, SINK_QUERIES, active_queries):
            continue
        upstream = get_instance_count(env, upstream_env_var, 1)
        watcher_targets.append((f"sink_{query_id}", HEALTH_PORT))
        lines.append(f"  sink_{query_id}:")
        lines.append(f"    image: sink_{query_id}")
        lines.append(f"    build:")
        lines.append(f"      context: .")
        lines.append(f"      dockerfile: cmd/Dockerfile")
        lines.append(f"      args:")
        lines.append(f"        SERVICE_PATH: sink")
        lines.append(f"    environment:")
        lines.append(f"      - QUERY_ID={query_id}")
        lines.append(f"      - INPUT_QUEUE={input_queue}")
        lines.append(f"      - OUTPUT_QUEUE=reports")
        lines.append(f"      - UPSTREAM_INSTANCES={upstream}")
        lines.append(f"      - RABBITMQ_HOST=rabbitmq")
        lines.append(f"      - RABBITMQ_PORT=5672")
        append_watcher_health_env(lines, watcher_enabled)
        lines.append(f"    depends_on:")
        lines.append(f"      rabbitmq:")
        lines.append(f"        condition: service_healthy")
        append_watcher_dependency(lines, watcher_enabled)
        lines.append("")

    # Named filters — instance count driven by .env, with specific build args and env vars
    for svc_name, filter_name, count_env_var, upstream_env_var, extra_env in NAMED_FILTERS:
        if not active_for(svc_name, NAMED_FILTER_QUERIES, active_queries):
            continue
        count = get_instance_count(env, count_env_var, 1)
        upstream = get_instance_count(env, upstream_env_var, 1) if upstream_env_var else 1
        for i in range(1, count + 1):
            svc_instance = f"{svc_name}_{i}"
            watcher_targets.append((svc_instance, HEALTH_PORT))
            lines.append(f"  {svc_instance}:")
            lines.append(f"    image: {svc_instance}")
            lines.append(f"    build:")
            lines.append(f"      context: .")
            lines.append(f"      dockerfile: cmd/Dockerfile")
            lines.append(f"      args:")
            lines.append(f"        SERVICE_PATH: filter/{filter_name}")
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
            append_watcher_health_env(lines, watcher_enabled)
            lines.append(f"    depends_on:")
            lines.append(f"      rabbitmq:")
            lines.append(f"        condition: service_healthy")
            append_watcher_dependency(lines, watcher_enabled)
            lines.append("")

    # Watcher — single instance, monitors all long-running services via TCP ping
    if watcher_enabled:
        services_str = ",".join(f"{name}:{port}" for name, port in watcher_targets)
        lines.append(f"  watcher:")
        lines.append(f"    image: watcher")
        lines.append(f"    build:")
        lines.append(f"      context: .")
        lines.append(f"      dockerfile: cmd/Dockerfile")
        lines.append(f"      args:")
        lines.append(f"        SERVICE_PATH: watcher")
        lines.append(f"    environment:")
        lines.append(f"      - COMPOSE_PROJECT=system")
        lines.append(f"      - WATCH_INTERVAL=15s")
        lines.append(f"      - PING_TIMEOUT=3s")
        lines.append(f"      - STARTUP_DELAY=30s")
        lines.append(f"      - SERVICES={services_str}")
        lines.append(f"    volumes:")
        lines.append(f"      - /var/run/docker.sock:/var/run/docker.sock")
        lines.append(f"    depends_on:")
        lines.append(f"      rabbitmq:")
        lines.append(f"        condition: service_healthy")
        lines.append("")

    return "\n".join(lines) + "\n"


def main():
    env = os.environ
    active_queries = get_active_queries(env)
    if active_queries != ALL_QUERIES:
        print(f"TEST_QUERIES={','.join(sorted(active_queries))} — including only queries: {sorted(active_queries)}")
    compose = build_compose(env, active_queries)
    COMPOSE_OUT.write_text(compose)
    print(f"Written {COMPOSE_OUT}")


if __name__ == "__main__":
    main()
