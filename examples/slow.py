"""Timeout test: sleeps past any short --timeout so the wall-clock kill can be seen."""
import time

for i in range(600):
    print(f"tick {i}", flush=True)
    time.sleep(1)
