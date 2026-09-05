# Interpreter benchmarks

`bench.py` is dependency-free and runs on any Python interpreter. Each
in-process workload is measured with `time.perf_counter` three times and the
minimum is printed in seconds. `run.py` additionally measures three fresh
`-c pass` processes from an external Python process.

From the repository root:

```sh
go build -o tmp/cpython-go ./cmd/cpython-go
/usr/bin/python3 internal/bench/run.py ./tmp/cpython-go
PYTHONPATH=tmp/darwin_arm64/cpython/Lib /usr/bin/python3 \
  internal/bench/run.py tmp/darwin_arm64/build/python.exe
```

Individual workloads can be selected by name, for example:

```sh
./tmp/cpython-go internal/bench/bench.py nbody regex
```

The suite is intentionally small. It is useful for before/after comparisons,
not as a comprehensive Python implementation score.
