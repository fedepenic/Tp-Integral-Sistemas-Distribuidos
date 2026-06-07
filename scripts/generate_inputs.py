import pandas as pd
import os
import shutil

sample_pct        = float(os.environ.get('DATASET_PERCENTAGE', 10))

# Use N_WORKERS override if set, otherwise use N_CLIENTS
n_workers = int(os.environ.get('N_WORKERS', 0))
n_clients = n_workers if n_workers > 0 else int(os.environ.get('N_CLIENTS', 5))
transactions_file = os.environ.get('TRANSACTIONS_FILE', 'LI-Medium_Trans.csv')
accounts_file     = os.environ.get('ACCOUNTS_FILE', 'LI-Medium_accounts.csv')

if os.path.exists("input"):
    for entry in os.scandir("input"):
        if entry.is_dir() and entry.name.startswith("client_"):
            shutil.rmtree(entry.path)

print("Loading source data...")
trans_df    = pd.read_csv(f"data/{transactions_file}")
accounts_df = pd.read_csv(f"data/{accounts_file}")

n_samples = max(1, int(len(trans_df) * sample_pct / 100))
print(f"Dataset size: {len(trans_df)} rows — sampling {sample_pct}% = {n_samples} transactions per client")

for i in range(1, n_clients + 1):
    client_dir = f"input/client_{i}"
    os.makedirs(client_dir, exist_ok=True)

    trans_df.sample(n=n_samples).to_csv(f"{client_dir}/{transactions_file}", index=False)
    accounts_df.to_csv(f"{client_dir}/{accounts_file}", index=False)

    print(f"Generated input for client_{i} ({n_samples} transactions)")

print("Done.")
