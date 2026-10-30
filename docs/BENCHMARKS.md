# Benchmarks

Measurements of the deployed dev stack under load. They show how the pipeline
behaves, not what it guarantees: each run is one sample on one day, against
one small document.

The tool is `scripts/bench-dev.bash`. It is manual and not part of CI,
because it uses real AWS.

## How it is measured

```sh
OUT=bench.csv bash scripts/bench-dev.bash                          # 20 jobs, 5 at a time
OUT=bench.csv JOBS=100 CONCURRENCY=20 bash scripts/bench-dev.bash  # the run below
```

- A throwaway Cognito user is created and deleted on exit.
- One warm-up job runs alone first and is left out of the numbers
  (`WARMUP=0` turns it off).
- Then `JOBS` documents are sent with up to `CONCURRENCY` in flight.
- Each job is timed from the end of its upload to a terminal status, by the
  client, polling once a second. A time is accurate to about a second and
  includes the API round trip.
- `JOBS` is capped at 100 and `CONCURRENCY` at 20. Nothing retries a job.
- With `OUT` set, one row per job goes to `bench.csv` and one row per run to
  `bench-summary.csv`. Both are appended to, so runs can be compared.

## Results

Document: a single-page PNG invoice, 1400x900, drawn by the script.

| Date (UTC) | Jobs | Concurrency | DONE | Failed | Wall | p50 | p90 | p95 | max |
|---|---|---|---|---|---|---|---|---|---|
| 2026-10-30 14:27 | 100 | 20 | 100 | 0 | 27.4s | 2.3s | 3.8s | 3.9s | 4.0s |

Throughput over the run was about 3.6 jobs a second.

### Reading the run

- **Nothing failed.** All 100 jobs reached `DONE`, with none failed or timed out.
- **Two clusters.** 78 jobs took under 3 seconds, mostly 2.0 to 2.5. The other
  22 took 3.1 to 4.0, and 16 of those took 3.6 or more. Those were mostly in
  the first wave of 20, and a few were in the last. The likely cause is cold
  starts, since 20 jobs at once force the `recognize` function to start new
  execution environments. That is an inference from the shape of the data;
  the function logs have not been checked.
- **No sign of a bottleneck at this load.** With 20 in flight the slowest job
  took 4 seconds, so the event, the queue, the starter, and the workflow did
  not visibly queue work.

## What this does not show

- **Other documents.** One small PNG ran. A multi-page PDF, a large image, or
  a dense scan will take longer, and nothing here predicts by how much.
- **A difference from the earlier smoke runs.** The smoke test saw about 12
  seconds for a PNG, with 6 to 12 seconds in OCR. This run's 2.3 seconds
  median is much lower. A warm function and a small image may explain it, but
  that has not been confirmed, so do not read 2 seconds as the cost of a
  document.
- **Cold start on its own.** The warm-up job is not recorded, so the first
  cold start is not in the CSV. Compare a run at low concurrency to isolate it.
- **Cost.** The workflow's transitions and the Lambda duration per job were
  not read back from AWS. The estimate of about five transitions a job comes
  from the state machine's definition, not from a measurement.
- **Limits.** Nothing was pushed to the point of throttling, so where the
  pipeline starts to queue or fail is unknown. The script stops at 100 jobs
  and 20 in flight on purpose.
- **Repeatability.** One run. The numbers move with the time of day, the
  Lambda's state, and the machine running the script, whose own uploads and
  polling share the same concurrency.

## Planned runs

| Run | Question it answers |
|---|---|
| The same load at `CONCURRENCY=5` | How much of the slow cluster is cold start |
| A two page PDF | What the PDF path costs under load |
| `recognize` at a different memory size | Whether more memory buys speed, since Lambda's CPU scales with it |

A memory change needs a `cdk deploy`, which is manual.
