import pandas as pd
import os
import shutil

sample_pct = float(os.environ.get('DATASET_PERCENTAGE', 10))
n_clients  = int(os.environ.get('N_CLIENTS', 5))

if os.path.exists("input"):
    for entry in os.scandir("input"):
        if entry.is_dir() and entry.name.startswith("client_"):
            shutil.rmtree(entry.path)

print("Loading source data...")
trans_df    = pd.read_csv("data/LI-Small_Trans.csv")
accounts_df = pd.read_csv("data/LI-Small_accounts.csv")

n_samples = max(1, int(len(trans_df) * sample_pct / 100))
print(f"Dataset size: {len(trans_df)} rows — sampling {sample_pct}% = {n_samples} transactions per client")

for i in range(1, n_clients + 1):
    client_dir = f"input/client_{i}"
    os.makedirs(client_dir, exist_ok=True)

    trans_df.sample(n=n_samples).to_csv(f"{client_dir}/LI-Small_Trans.csv", index=False)
    accounts_df.to_csv(f"{client_dir}/LI-Small_accounts.csv", index=False)

    print(f"Generated input for client_{i} ({n_samples} transactions)")

print("Done.")
