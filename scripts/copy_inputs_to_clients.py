import os
import shutil
import sys


FILES_BY_SIZE = {
    "small": ("LI-Small_accounts.csv", "LI-Small_Trans.csv"),
    "medium": ("LI-Medium_accounts.csv", "LI-Medium_Trans.csv"),
}


def main():
    if len(sys.argv) != 2 or sys.argv[1] not in FILES_BY_SIZE:
        valid_sizes = ", ".join(sorted(FILES_BY_SIZE))
        print(f"Usage: python scripts/copy_inputs_to_clients.py <{valid_sizes}>")
        sys.exit(1)

    size = sys.argv[1]
    base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    data_dir = os.path.join(base_dir, "data", f"input_100_{size}")
    input_dir = os.path.join(base_dir, "input")
    source_files = [os.path.join(data_dir, file_name) for file_name in FILES_BY_SIZE[size]]

    if not os.path.isdir(data_dir):
        print(f"ERROR: {data_dir} not found.")
        sys.exit(1)

    for source_file in source_files:
        if not os.path.isfile(source_file):
            print(f"ERROR: {source_file} not found.")
            sys.exit(1)

    os.makedirs(input_dir, exist_ok=True)

    n_workers = int(os.environ.get("N_WORKERS", 0))
    n_clients = n_workers if n_workers > 0 else int(os.environ.get("N_CLIENTS", 5))

    for i in range(1, n_clients + 1):
        client_dir = os.path.join(input_dir, f"client_{i}")
        os.makedirs(client_dir, exist_ok=True)

        for entry in os.scandir(client_dir):
            if entry.is_dir():
                shutil.rmtree(entry.path)
            else:
                os.remove(entry.path)

        for source_file in source_files:
            shutil.copy2(source_file, os.path.join(client_dir, os.path.basename(source_file)))

        print(f"Copied {len(source_files)} files to {client_dir}")


if __name__ == "__main__":
    main()
