# Load test (slo.md "Load test plan")

Local only: never run against a live host. Everything lives in a temp dir
(`LT_DIR`, default `/tmp/market-loadtest`).

    scripts/loadtest/loadtest.sh "label" -c 16 -d 20s      # all scenarios
    scripts/loadtest/loadtest.sh "label" -only "home,product"

- `seed` (once per `LT_DIR`): 10 000 products (about 95% published, about
  10% free), 500 active sellers, 12 categories, one offer and one
  published release per product, 50 buyer accounts with a throw-away
  password stored in the temp dir. Refuses a dir that already has a DB.
- The binary runs pinned to CPU 0 with `GOMAXPROCS=1` (the 1 vCPU host),
  fake gateway, file mailer, metrics on `127.0.0.1:19091`.
- `run`: closed loop of `-c` workers for `-d` per scenario; client-side
  p50/p95/p99, RPS, errors, plus server-side statements per request, DB
  time and templ render time from `/metrics`, and RSS, heap, goroutines
  and writer-connection wait before and after.
- `explain -db COPY/data.db [-analyze]`: EXPLAIN QUERY PLAN and timing of
  the main storefront queries.

Results: `.project/loadtest/`.
