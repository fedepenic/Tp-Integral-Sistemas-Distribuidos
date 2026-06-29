# Tp-Integral-Sistemas-Distribuidos
Repositorio correspondiente al trabajo práctico de la materia 75.74 Sistemas Distribuidos I

## Diagrams

For a better understanding of the architecture and data flow, refer to the diagrams in `docs/diagrams/`:

| Diagram | File |
|---|---|
| Processing pipeline DAG | `docs/diagrams/Diagrama DAG.drawio.png` |
| Deployment | `docs/diagrams/Diagrama-de-despliegue.png` |
| Packages | `docs/diagrams/Diagrama-de-paquetes.png` |
| Robustness | `docs/diagrams/diagrama-robustez.html` |
| Sequence diagrams (per query) | `docs/diagrams/Diagramas de Secuencia/` |
| Activity diagrams (per query) | `docs/diagrams/Diagramas de Actividad/` |

For a deeper understanding of the fault tolerance design and decisions, refer to the report at `assignments/TP Tolerancia a Fallas - Money Laundering Analysis.pdf`.

## Make commands

Place the dataset files inside the `data/` folder (not tracked by git), then use the commands below.

### One-step pipelines

| Command | Description |
|---|---|
| `make all` | Full pipeline: build → clean output → generate compose → generate inputs → run notebook + system |
| `make all-notebook` | Notebook pipeline: build → clean output → generate inputs → run notebook analysis |
| `make all-notebook-local` | Same as `all-notebook` but runs locally without Docker |
| `make all-system` | System pipeline: build → clean output → generate compose → generate inputs → start distributed system |
| `make all-system-demo` | Demo pipeline: build → clean output → generate compose → copy inputs/results → start system |

### Cleanup

| Command | Description |
|---|---|
| `make clean-output` | Removes all generated output files (via Docker) |
| `make clean-output-local` | Same as `clean-output` but runs locally |
| `make clean-client-progress` | Deletes `.progress` files from the `input/` directory (via Docker) |
| `make clean-client-progress-local` | Same as `clean-client-progress` but runs locally |
| `make clean-system-output` | Removes system output files and `.progress` files from `input/` |

### Individual steps

| Command | Description |
|---|---|
| `make build` | Builds the Docker image |
| `make generate-compose` | Generates `system/docker-compose.yml` from `.env` configuration |
| `make generate-inputs` | Generates input files for each client from the dataset (via Docker) |
| `make generate-inputs-local` | Same as `generate-inputs` but runs locally |
| `make run-notebook` | Runs the notebook analysis for all clients (via Docker) |
| `make run-notebook-local` | Same as `run-notebook` but runs locally |
| `make run-system` | Stops any running containers, then starts the distributed system via docker compose |
| `make run-system-only` | Cleans system output and starts the distributed system (skips input generation) |
| `make copy-inputs-to-clients` | Copies dataset inputs into each client's directory (via Docker) |
| `make copy-inputs-to-clients-local` | Same as `copy-inputs-to-clients` but runs locally |
| `make copy-results-to-notebook` | Copies system results into the notebook output directory for comparison (via Docker) |
| `make copy-results-to-notebook-local` | Same as `copy-results-to-notebook` but runs locally |

Variables from `.env` are used automatically. You can override them inline:

```bash
make generate-inputs N_CLIENTS=3
```

### Comparison

| Command | Description |
|---|---|
| `make compare` | Compares notebook and system outputs to validate correctness |
| `make compare <dataset>` | Compares outputs for a specific dataset (e.g. `make compare small`) |

### Utilities

| Command | Description |
|---|---|
| `make monitor-services` | Continuously prints `docker ps` output every second |
| `make chaos` | Runs the chaos testing script locally |
| `make kill <service>` | Sends SIGKILL to a specific docker compose service (e.g. `make kill client`) |
| `make prune` | Removes dangling Docker images |

### Teardown

| Command | Description |
|---|---|
| `make stop-system` | Stops and removes the distributed system containers (with volumes) |
| `make down` | Stops any running `money-laundering` containers |

### Output

Results will be saved in `output/notebook/client_N/` and `output/system/client_N/` for each client:

| File | Description |
|---|---|
| `query_1.csv` | Transactions under 50 USD |
| `query_2.csv` | Max transaction amount per source bank |
| `query_3.csv` | Transactions below 1% of average amount in prior period |
| `query_4.csv` | Accounts matching the scatter-gather pattern |
| `query_5.csv` | Wire/ACH transactions with converted amount under USD 1 |
