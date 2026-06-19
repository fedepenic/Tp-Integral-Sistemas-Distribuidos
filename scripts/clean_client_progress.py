import os


def is_progress_file(name):
    return name.startswith(".") and name.endswith(".progress")


if not os.path.exists("input"):
    print("input/ not found, nothing to do.")
else:
    removed = 0
    for entry in os.scandir("input"):
        if not entry.is_dir() or not entry.name.startswith("client_"):
            continue

        for client_entry in os.scandir(entry.path):
            if client_entry.is_file() and is_progress_file(client_entry.name):
                os.remove(client_entry.path)
                removed += 1

    print(f"Cleared {removed} client progress file(s).")
