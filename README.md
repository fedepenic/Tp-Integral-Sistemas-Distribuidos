# Tp-Integral-Sistemas-Distribuidos
Repositorio correspondiente al trabajo práctico de la materia 75.74 Sistemas Distribuidos I

## Running the analysis

Place the dataset files inside the `data/` folder (not tracked by git), then:

```bash
make build   # builds the Docker image
make run     # runs the analysis and saves results to output/
make all     # build + run in one step
make down    # stops the running container
```

Results will be saved in the `output/` folder:

| File | Description |
|---|---|
| `query_1.csv` | Transactions under 50 USD |
| `query_2.csv` | Max transaction amount per source bank |
| `query_3.csv` | Transactions below 1% of average amount in prior period |
| `query_4.csv` | Accounts matching the scatter-gather pattern |
| `query_5.csv` | Wire/ACH transactions with converted amount under USD 1 |
