import os
import shutil
import sys


VALID_SIZES = {"small", "medium"}


def main():
    size = sys.argv[1] if len(sys.argv) == 2 else os.environ.get("DATASET", "")
    size = size.lower()

    if size not in VALID_SIZES:
        valid_sizes = ", ".join(sorted(VALID_SIZES))
        print(f"Usage: python scripts/copy_results_to_notebook.py <{valid_sizes}>")
        print("Or set DATASET in .env.")
        sys.exit(1)

    base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    source_dir = os.path.join(base_dir, "data", f"results_100_{size}")
    notebook_dir = os.path.join(base_dir, "output", "notebook")

    if not os.path.isdir(source_dir):
        print(f"ERROR: {source_dir} not found.")
        sys.exit(1)

    os.makedirs(notebook_dir, exist_ok=True)

    source_files = [
        entry
        for entry in sorted(os.scandir(source_dir), key=lambda e: e.name)
        if entry.is_file()
    ]

    if not source_files:
        print(f"ERROR: {source_dir} has no files to copy.")
        sys.exit(1)

    client_dirs = [
        entry
        for entry in sorted(os.scandir(notebook_dir), key=lambda e: e.name)
        if entry.is_dir()
    ]

    if not client_dirs:
        n_workers = int(os.environ.get("N_WORKERS", 0))
        n_clients = n_workers if n_workers > 0 else int(os.environ.get("N_CLIENTS", 5))
        for i in range(1, n_clients + 1):
            os.makedirs(os.path.join(notebook_dir, f"client_{i}"), exist_ok=True)

        client_dirs = [
            entry
            for entry in sorted(os.scandir(notebook_dir), key=lambda e: e.name)
            if entry.is_dir()
        ]

    for client_dir in client_dirs:
        for source_file in source_files:
            destination = os.path.join(client_dir.path, source_file.name)
            shutil.copy2(source_file.path, destination)
        print(f"Copied {len(source_files)} files to {client_dir.path}")


if __name__ == "__main__":
    main()
