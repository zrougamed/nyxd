# Load and failure testing

`scripts/stress.sh` puts a real nyxd under load and through failures, then checks that it
recovers and does not leak. It runs in CI (the **Stress** workflow: weekly, on demand, and on
pull requests that touch the supervisor, logs or runtime), rootful and rootless, and writes
its measurements to the job summary.

```bash
make build build-nyx
sudo scripts/stress.sh        # rootful: every scenario
scripts/stress.sh             # rootless: everything but the OOM scenario
```

## Scenarios

| Scenario | What it does | Fails when |
|----------|--------------|------------|
| Many at once | Starts `STRESS_CONTAINERS` (50) long-running containers from `STRESS_PARALLEL` (8) clients, execs into some, removes them all | any start/exec/remove fails; containers, bundles or logs are left behind |
| Churn | Runs `STRESS_CHURN` (200) short-lived containers | a container did not run; daemon RSS grows by more than `STRESS_RSS_GROWTH` MiB (40) or its open files by more than `STRESS_FD_GROWTH` (16); bundles are left behind |
| Log flood | A container writes as fast as it can for `STRESS_LOG_SECONDS` (20) with `-log-max-size 1m -log-max-files 2` | logs on disk exceed the limit; `nyx logs --tail` takes 2 s or more |
| Out of memory (rootful) | A Compose service with a 32 MiB memory limit allocates 1 GiB, next to a second service | the hog is not OOM-killed (exit 137); the other service or the daemon stops |
| Daemon crash | `SIGKILL`s nyxd with 10 containers running and starts it again | the containers are not all re-adopted within 30 s, or exec into them fails |
| Large image (optional) | `STRESS_BIG_IMAGE=<ref>` pulls and runs a large image | the pull or run fails |

It reports the daemon's RSS at idle, under load and at its peak, start/remove/churn times,
log sizes and re-adopt time. Set `STRESS_REPORT=<file>` for a Markdown copy.

Run it by hand on the Actions tab (**Stress** → *Run workflow*) with a different number of
containers, churn count or a large image to pull.
