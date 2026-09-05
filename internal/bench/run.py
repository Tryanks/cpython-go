"""Run bench.py and measure cold process startup for an interpreter."""

import os
import subprocess
import sys
import time


def main():
    if len(sys.argv) < 2:
        raise SystemExit("usage: run.py INTERPRETER [INTERPRETER_ARG ...]")
    command = sys.argv[1:]
    bench = os.path.join(os.path.dirname(os.path.abspath(__file__)), "bench.py")
    subprocess.run(command + [bench], check=True)

    samples = []
    for _ in range(3):
        start = time.perf_counter()
        subprocess.run(command + ["-c", "pass"], check=True,
                       stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                       stderr=subprocess.DEVNULL)
        samples.append(time.perf_counter() - start)
    print("%-18s %.6f" % ("startup", min(samples)))


if __name__ == "__main__":
    main()
