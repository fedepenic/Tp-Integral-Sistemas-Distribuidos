import os
import shutil

if not os.path.exists("output"):
    print("output/ not found, nothing to do.")
else:
    for entry in os.scandir("output"):
        if entry.is_dir():
            shutil.rmtree(entry.path)
        else:
            os.remove(entry.path)
    print("Cleared output/")
