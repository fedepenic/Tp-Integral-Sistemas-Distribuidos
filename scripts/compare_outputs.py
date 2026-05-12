import os
import sys
import pandas as pd

base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
notebook_dir = os.path.join(base_dir, "output", "notebook")
system_dir = os.path.join(base_dir, "output", "system")

if not os.path.exists(notebook_dir):
    print("ERROR: output/notebook/ not found. Run 'make run' first.")
    sys.exit(1)

if not os.path.exists(system_dir):
    print("ERROR: output/system/ not found.")
    sys.exit(1)

all_match = True

for client in sorted(os.scandir(notebook_dir), key=lambda e: e.name):
    if not client.is_dir():
        continue

    sys_client_dir = os.path.join(system_dir, client.name)
    if not os.path.exists(sys_client_dir):
        print(f"{client.name}: MISSING in system output")
        all_match = False
        continue

    for query_file in sorted(os.listdir(client.path)):
        nb_path = os.path.join(client.path, query_file)
        sys_path = os.path.join(sys_client_dir, query_file)

        if not os.path.exists(sys_path):
            print(f"{client.name}/{query_file}: MISSING in system output")
            all_match = False
            continue

        nb_df = pd.read_csv(nb_path)
        sys_df = pd.read_csv(sys_path)

        cols = nb_df.columns.tolist()
        nb_sorted = nb_df.sort_values(by=cols).reset_index(drop=True)
        sys_sorted = sys_df.sort_values(by=cols).reset_index(drop=True)

        if nb_sorted.equals(sys_sorted):
            print(f"{client.name}/{query_file}: OK")
        else:
            print(f"{client.name}/{query_file}: DIFFERENT")
            all_match = False

print()
if all_match:
    print("All outputs match.")
else:
    print("Some outputs differ.")
    sys.exit(1)
