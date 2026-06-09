import os
import sys
import pandas as pd

base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
notebook_dir = os.path.join(base_dir, "output", "notebook")
system_dir = os.path.join(base_dir, "output", "system")

query_filter = None
if len(sys.argv) > 1:
    query_filter = f"query_{sys.argv[1]}.csv"

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
        if query_filter and query_file != query_filter:
            continue

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
            merged = nb_sorted.merge(sys_sorted, how="outer", indicator=True)
            only_nb_df = merged[merged["_merge"] == "left_only"].copy()
            only_sys_df = merged[merged["_merge"] == "right_only"].copy()
            
            print(f"\n{client.name}/{query_file}: DIFFERENT")
            print(f"  notebook_only={len(only_nb_df)} system_only={len(only_sys_df)}")
            
            # Imprimir líneas solo en notebook
            if len(only_nb_df) > 0:
                print(f"\n  --- Lines only in NOTEBOOK ({len(only_nb_df)}): ---")
                only_nb_display = only_nb_df.drop(columns=['_merge'])
                print(only_nb_display.to_string(index=False))
            
            # Imprimir líneas solo en system
            if len(only_sys_df) > 0:
                print(f"\n  --- Lines only in SYSTEM ({len(only_sys_df)}): ---")
                only_sys_display = only_sys_df.drop(columns=['_merge'])
                print(only_sys_display.to_string(index=False))
            
            # También comparar fila por fila si tienen el mismo número de filas
            if len(nb_sorted) == len(sys_sorted):
                # Comparar fila por fila
                for idx in range(len(nb_sorted)):
                    if not nb_sorted.iloc[idx].equals(sys_sorted.iloc[idx]):
                        print(f"\n  --- Row {idx} differs: ---")
                        print(f"    Notebook: {nb_sorted.iloc[idx].to_dict()}")
                        print(f"    System:   {sys_sorted.iloc[idx].to_dict()}")
            
            all_match = False

print()
if all_match:
    print("All outputs match.")
else:
    print("Some outputs differ.")
    sys.exit(1)