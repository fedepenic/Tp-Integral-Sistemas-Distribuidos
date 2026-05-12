import os
import shutil
import subprocess

base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
n_clients = int(os.environ.get('N_CLIENTS', 5))

notebook_output_root = os.path.join(base_dir, "output", "notebook")
if os.path.exists(notebook_output_root):
    for entry in os.scandir(notebook_output_root):
        if entry.is_dir() and entry.name.startswith("client_"):
            shutil.rmtree(entry.path)

for i in range(1, n_clients + 1):
    input_dir = os.path.join(base_dir, f"input/client_{i}")
    output_dir = os.path.join(base_dir, "output", "notebook", f"client_{i}")
    os.makedirs(output_dir, exist_ok=True)

    print(f"Running analysis for client_{i}...")
    env = {**os.environ, 'INPUT_DIR': input_dir, 'OUTPUT_DIR': output_dir}
    subprocess.run(
        [
            "jupyter", "nbconvert", "--to", "notebook", "--execute",
            "--ExecutePreprocessor.timeout=600",
            "scripts/money-laundering-analysis.ipynb"
        ],
        env=env,
        check=True
    )
    print(f"Done for client_{i}")
