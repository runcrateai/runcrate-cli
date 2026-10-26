# Runcrate CLI

The official command-line interface for [Runcrate](https://runcrate.ai) — deploy
and manage GPU instances, run remote commands over keyless SSH, copy files, and
manage workspaces, storage, templates, and billing from your terminal.

## Install

**macOS / Linux:**

```bash
curl -sSL https://runcrate.ai/install.sh | sh
```

**Homebrew (macOS / Linux):**

```bash
brew install runcrateai/tap/runcrate
```

**Windows (PowerShell):**

```powershell
irm https://runcrate.ai/install.ps1 | iex
```

Binaries are published to this repo's [GitHub Releases](https://github.com/runcrateai/runcrate-cli/releases)
for linux/macOS (amd64 + arm64) and windows (amd64). `rc` is installed as a
shortcut alias for `runcrate`.

## Getting started

```bash
runcrate login                 # browser OAuth
runcrate instances             # list instances
runcrate ssh my-gpu            # interactive shell (ephemeral SSH cert)
runcrate ssh my-gpu -- nvidia-smi
runcrate cp ./train.py my-gpu:/root/workspace/
```

For unattended or agent-run work, give the instance an auto stop so it does not keep
billing after the job ends. Durations are whole minutes written like `30m`, `4h` or
`1h30m`. `--max-runtime` accepts 5m to 168h, `--idle-stop` accepts 5m to 24h (idle
means GPU use under 5% and nobody connected over SSH, the web terminal or the editor; GPU instances only). Auto stop terminates the instance.

```bash
runcrate instances create --name train --gpu H100 --max-runtime 4h --idle-stop 30m
runcrate instances info train   # Max runtime, Idle stop, Auto stop at, Stop reason

# Extend it before a long job (total time from start), or turn a limit off
runcrate instances auto-stop train --max-runtime 8h --idle-stop off
```

Point the CLI at a different API origin with `runcrate config set url <url>` or the
`RUNCRATE_API_URL` env var (defaults to `https://runcrate.ai`).

## Jobs

A job runs one command on its own GPU machine, streams its output, and stops the
machine (and its billing) when the command ends.

```bash
runcrate run --gpu A4000 -- nvidia-smi
runcrate run --gpu H100 --volume <volume-id> --max-runtime 2h -- python train.py --epochs 3
runcrate run --gpu A4000 --env HF_TOKEN -d -- 'pip install -r requirements.txt && python eval.py'
runcrate run --gpu H100,A100-80GB --code . --retries 2 --tag project=vision -- python train.py
runcrate run --gpu L40S --params sweep.json --max-parallel 4 -- 'python train.py --lr $LR'
runcrate jobs list [--status running,queued] [--tag k=v] [--group <id>] [--since 24h] [--json]
runcrate jobs status <id>      # exit code, error, cost, attempts, tags, code, outputs
runcrate jobs logs <id> [-f]   # print output; -f follows until the job ends
runcrate jobs wait <id> [--timeout 2h]
runcrate jobs artifacts <id>   # files the command wrote to $RUNCRATE_OUTPUT_DIR
runcrate jobs download <id> [path...] [-o dir]
runcrate jobs rerun <id> [--gpu H100] [--env KEY=VALUE]
runcrate jobs cancel <id>
runcrate jobs delete <id>
runcrate jobs group status <group-id>
runcrate jobs group cancel <group-id> [--yes]   # asks first: it stops every live machine
```

- The command runs with bash in `/workspace`, which holds the volume's files when
  `--volume` is set; files written there are copied back when the machine stops.
  With `--code <dir>` (uploaded as a tar.gz without `.git`, `__pycache__` and
  `node_modules`) or `--git <url>` it runs in `/workspace/code`.
- `--max-runtime` takes `90m`, `2h`, `1h30m` or minutes, from 1m to 168h (default 1h),
  per attempt.
- `--gpu H100,A100-80GB:2` tries the GPUs in order; `--capacity-wait` (0 to 24h)
  says how long to wait for one.
- `--retries N` retries a failed machine on a new one; add `--retry-on nonzero_exit`
  (and `--retry-exit-codes 137`) to retry the command's own failures.
- `--count N` or `--params file.json` (a list of env objects) start a group of jobs,
  at most `--max-parallel` at once; `run` prints their IDs and returns.
- `--env KEY=VALUE` is repeatable; `--env KEY` copies the value from your shell.
  Values are stored only encrypted and never shown; `jobs rerun` reuses them.
- Every submit sends an Idempotency-Key; pass `--idempotency-key` to make a script
  safe to run twice (the second run answers the first job for 24 hours). When the
  answer to a submit is lost, the error prints the key it sent: re-run with it.
- `runcrate run`, `runcrate jobs logs -f` and `runcrate jobs wait` exit with the job's
  code: the command's own code, 124 on timeout, 130 when cancelled (`jobs wait` exits
  75 when its `--timeout` passes first). Ctrl-C detaches and leaves the job running;
  `-d` / `--detach` prints the job ID and returns.

## Schedules

A schedule starts an instance or a job later: once with `--at`, or on a recurring
5-field `--cron`. A command after `--` makes it a job (the `runcrate run` flags from
`--gpu` to `--region`; retries, groups, tags and code are for `runcrate run` only);
without one it starts an instance (same flags as `runcrate instances create`, so
`--name` is required). `--max-runtime` is required for both, so nothing a schedule
starts keeps running unattended.

```bash
runcrate schedule create --cron '0 2 * * *' --tz Europe/London --name nightly-train \
  --gpu H100 --volume <volume-id> --max-runtime 2h -- python train.py
runcrate schedule create --at '2026-10-21 09:00' --name morning-box \
  --gpu A4000 --max-runtime 8h --idle-stop 30m
runcrate schedule list [-q] [--json]   # when, next run (your local time), last result
runcrate schedule get <id>             # details, last run, last error
runcrate schedule pause <id>           # resume <id> turns it back on
runcrate schedule run-now <id>         # start it now; the next run stays as it is
runcrate schedule delete <id> [--yes]  # what it already started keeps running
```

- `--at '2026-10-21 02:00'` is the wall time in `--tz`; a time with an offset
  (`2026-10-21T02:00:00+01:00`) is used as is. `--tz` takes an IANA name and
  defaults to this machine's timezone, for `--cron` too.
- Across daylight saving: a time the clocks skip moves forward by the gap (02:30
  becomes 03:30), and a time that happens twice runs the first time.
- Recurring runs are at least 15 minutes apart. `--overlap skip` (the default)
  skips a run while the previous one is still going; `--overlap allow` starts it.
- Runs start within about 2 minutes of their time, as you, in the current
  environment. While the environment's monthly budget is reached, runs are skipped.
- A schedule set up with an API key or an agent sign-in launches with it; when
  that key is revoked or the sign-in ends, the schedule pauses (last result
  `paused: key revoked`).

## Clusters

A cluster is several GPU machines on one private network, with SSH between them,
`/shared` on every node, and optionally Slurm, Kubernetes or Ray installed across
them. It is bought by the machine: each machine has 1, 2, 4 or 8 GPUs, and the
price is per hour for the whole cluster, with the price per machine under it. It is
all or nothing: every machine starts or none does, and a cluster that could not
start costs nothing. Billing starts when every machine is up and runs until you
delete the cluster, which stops every machine together.

```bash
runcrate clusters offers --gpu H100 --nodes 4        # the machines it can start on: $/hr per machine and for the cluster
runcrate clusters offers --gpu H100:8 --nodes 4      # only machines with 8 GPUs each
runcrate clusters create --name train --offer <offer> --nodes 4 --stack slurm
runcrate clusters list [--all] [-q] [--json]
runcrate clusters get train                          # machines, hostfile, torchrun / mpirun / sbatch, events
runcrate clusters ssh train [--node 2] [-- sinfo]    # the head is node 0
runcrate clusters kubeconfig train -o kubeconfig.yaml  # Kubernetes clusters
runcrate clusters tunnel train                       # Kubernetes API on 6443, Ray dashboard on 8265
runcrate clusters test train [-d]                    # NCCL all-reduce bandwidth across every GPU
runcrate clusters delete train [--yes]               # terminates every machine
```

- `--stack` is `none` (SSH, hostfile and torchrun only), `slurm`, `kubernetes` or `ray`;
  `offers` lists the ones each offer can run.
- Self-serve clusters run on Ethernet: good for independent jobs, Slurm job arrays
  and Ray tasks, not for training one model across machines. Clusters on a fast
  InfiniBand or RoCE network are coming; ask us for one.
- Offers are on datacenter hosts unless you pass `--include-community` (to both
  `offers` and `create`). With `create --volume`, list offers with `--with-volume`.

## Secrets

Secrets are environment variables every instance and job launched in an
environment gets, such as `HF_TOKEN`. The CLI shows their names, never their
values; a workspace owner or manager sets them in the dashboard
(https://runcrate.ai/dashboard/settings/secrets).

```bash
runcrate secrets list [-q] [--json]   # names in the current environment, sensitive or not, last change
runcrate envs switch staging && runcrate secrets list
```

A value passed for one launch (`runcrate run --env HF_TOKEN=...`) wins over the
secret of the same name.

## AI clients (MCP)

Runcrate runs a remote MCP server at `https://mcp.runcrate.ai/mcp`. The quickest
way in is one line pasted into your agent (Claude Code, Codex, Cursor, VS Code,
OpenCode or any agent that runs commands). It adds the server to itself, and the
first Runcrate tool call opens your browser to sign in. No key to paste.

```text
Add Runcrate's MCP server to yourself: run curl -fsSL "https://runcrate.ai/mcp.md" and follow it.
```

From a terminal, one command adds it to every AI client on this machine instead:

```bash
runcrate mcp install                            # every client found on this machine
runcrate mcp install --client cursor --dry-run  # show the change, write nothing
runcrate mcp install --client claude,codex
runcrate mcp url                                # print the server URL
```

- Clients: `cursor`, `claude` (Claude Code), `vscode`, `codex`, `windsurf` (and
  Devin Desktop), `gemini`, or `all` (the default).
- Where a client has its own `mcp add` command it is used; otherwise its config
  file is merged. Other servers stay, and the original is backed up next to it as
  `<file>.bak-<YYYYMMDD-HHMMSS>`.
- A file with comments or other non-JSON content is never written: the snippet to
  add is printed instead. An existing `runcrate` entry that points somewhere else
  is kept unless you pass `--force`.
- Codex: run `codex mcp login runcrate` if it has not signed you in yet.
- Setup guides for other clients: https://runcrate.ai/docs/mcp/setup

## Updating

```bash
runcrate update                # self-update to the latest release
# Homebrew installs: brew upgrade runcrate
```

## Development

```bash
make build                     # build ./bin/runcrate
make test                      # go test ./...
make build-all                 # cross-compile all platforms into ./bin
```

## Releasing

Releases are tag-driven. Pushing a `vX.Y.Z` tag runs `.github/workflows/release.yml`,
which tests, cross-compiles the five targets, generates `checksums.txt`, and
publishes a GitHub Release. The public installer scripts and `runcrate update`
read these releases, so no separate publish step is needed. The same workflow asks
[runcrateai/homebrew-tap](https://github.com/runcrateai/homebrew-tap) to regenerate its
formula from the new checksums (needs the `HOMEBREW_TAP_TOKEN` secret; without it the
tap's own six-hourly sync picks the release up).

```bash
git tag v0.1.0
git push origin v0.1.0
```

> This repo is consumed by the Runcrate monorepo as a git submodule
> (`packages/cli`). Pushing to `main` notifies the monorepo to bump the
> submodule pointer.

## License

MIT
