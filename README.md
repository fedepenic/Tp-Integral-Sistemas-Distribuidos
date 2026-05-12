# Tp-Integral-Sistemas-Distribuidos
Repositorio correspondiente al trabajo práctico de la materia 75.74 Sistemas Distribuidos I

## Running the analysis

Place the dataset files inside the `data/` folder (not tracked by git), then:

```bash
make build                        # builds the Docker image
make generate-inputs              # generates sampled input files for each client (default: 500000 samples, 5 clients)
make generate-inputs N_SAMPLES=100000 N_CLIENTS=3  # override defaults
make run                          # runs the analysis for all clients
make all                          # build + generate-inputs + run in one step
make down                         # stops the running container
```

Results will be saved in `output/client_N/` for each client:

| File | Description |
|---|---|
| `query_1.csv` | Transactions under 50 USD |
| `query_2.csv` | Max transaction amount per source bank |
| `query_3.csv` | Transactions below 1% of average amount in prior period |
| `query_4.csv` | Accounts matching the scatter-gather pattern |
| `query_5.csv` | Wire/ACH transactions with converted amount under USD 1 |
