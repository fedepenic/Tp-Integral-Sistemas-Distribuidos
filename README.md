# Tp-Integral-Sistemas-Distribuidos
Repositorio correspondiente al trabajo práctico de la materia 75.74 Sistemas Distribuidos I

## Make commands

Place the dataset files inside the `data/` folder (not tracked by git), then use the commands below.

### One-step pipelines

| Command | Description |
|---|---|
| `make all-notebook` | Full notebook pipeline: build → generate inputs → run notebook analysis |
| `make all-system` | Full system pipeline: build → generate compose → generate inputs → start distributed system |

### Comparison

| Command | Description |
|---|---|
| `make compare` | Compares notebook and system outputs to validate correctness |

### Individual steps

| Command | Description |
|---|---|
| `make build` | Builds the Docker image |
| `make generate-compose` | Generates `system/docker-compose.yml` from `.env` configuration |
| `make generate-inputs` | Generates sampled input files for each client (default: 500000 samples, 5 clients) |
| `make run-notebook` | Runs the Jupyter notebook analysis for all clients |
| `make run-system` | Starts the distributed system (client + all nodes) via docker compose |

You can override defaults when generating inputs:

```bash
make generate-inputs N_SAMPLES=100000 N_CLIENTS=3
```

### Teardown

| Command | Description |
|---|---|
| `make stop-system` | Stops and removes the distributed system containers |
| `make down` | Stops any running notebook analysis containers |

### Output

Results will be saved in `output/notebook/client_N/` and `output/system/client_N/` for each client:

| File | Description |
|---|---|
| `query_1.csv` | Transactions under 50 USD |
| `query_2.csv` | Max transaction amount per source bank |
| `query_3.csv` | Transactions below 1% of average amount in prior period |
| `query_4.csv` | Accounts matching the scatter-gather pattern |
| `query_5.csv` | Wire/ACH transactions with converted amount under USD 1 |
