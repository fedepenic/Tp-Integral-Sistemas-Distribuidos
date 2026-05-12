import pandas as pd
import os
import shutil

n_samples = int(os.environ.get('N_SAMPLES', 500000))
n_clients = int(os.environ.get('N_CLIENTS', 5))

if os.path.exists("input"):
    for entry in os.scandir("input"):
        if entry.is_dir() and entry.name.startswith("client_"):
            shutil.rmtree(entry.path)

print("Loading source data...")
trans_df = pd.read_csv("data/LI-Small_Trans.csv")
accounts_df = pd.read_csv("data/LI-Small_accounts.csv")

for i in range(1, n_clients + 1):
    client_dir = f"input/client_{i}"
    os.makedirs(client_dir, exist_ok=True)

    trans_df.sample(n=min(n_samples, len(trans_df))).to_csv(f"{client_dir}/LI-Small_Trans.csv", index=False)
    accounts_df.to_csv(f"{client_dir}/LI-Small_accounts.csv", index=False)

    print(f"Generated input for client_{i} ({n_samples} transactions)")

print("Done.")
