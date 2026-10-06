# muda

muda finds where your delivery pipelines waste time, and fixes it with evidence.

*Muda* (無駄) is Japanese for "waste". The tool reads your GitHub Actions
history, shows where the minutes go, and names the waste behind them. Then it
helps an agent fix one finding at a time, in a pull request you review, and
proves the result on the real pipeline.

The loop is **measure → diagnose → fix → verify**:

1. **Measure.** Where does the delivery time go? Every number cites its runs,
   jobs and steps.
2. **Diagnose.** Which waste explains each time sink? Each finding is labelled
   `measured` or `suspected`.
3. **Fix.** One finding per pull request, approved by a person before any edit.
4. **Verify.** Before and after timings on the real pipeline, and proof that no
   gate got weaker.

## Install

The binary:

```sh
curl -fsSL https://muda.tools/install.sh | sh
```

`muda update` moves it to the latest release later.

The skills come with the binary. Install them for your agent:

```sh
muda skills install --agent claude --scope user
```

Agents are `claude`, `pi` and `codex`. Scopes are `user` (the default) and
`project` (the Git root of the current repository).

You can also get the skills from this repository:

- **Claude Code plugin:** `/plugin marketplace add schuettc/muda`, then
  `/plugin install muda@muda`.
- **pi package:** `pi install git:github.com/schuettc/muda`.

A plugin or package brings the skills, not the binary. Install the binary
first. Each skill checks for a compatible muda before it starts.

## Quickstart

muda needs a GitHub token. It uses `GH_TOKEN`, then `GITHUB_TOKEN`, then
`gh auth token`.

Most people start through their agent. In a clone of the repository, ask it to use the
`muda` skill. It confirms the baseline and the gates with you, then walks the
loop.

The commands also work on their own:

```sh
cd your-repo
muda measure --since 30d --format md   # where the time goes
muda scan --format md                  # static workflow signals
muda gates --format md                 # the gates a fix must keep
muda notices --format md               # warnings you may have missed
muda recipes                           # the fix library
```

`muda commands --json` lists every command. `muda help <command>` explains one.

## The five skills

| Skill | What it does |
|---|---|
| `muda` | The entry point. Checks the binary, reads `.muda/`, and routes to the next step. |
| `muda-measure` | Records a baseline: roles, stacks, the gate inventory and the window, confirmed with you. |
| `muda-diagnose` | Turns the baseline into findings with evidence, an order and any exemptions. |
| `muda-fix` | Plans an approved finding's change, makes a branch and opens a pull request. |
| `muda-verify` | Checks the effect and the gate result on the pull request, and again after merge. |

## Waste classes and recipes

The standard names ten waste classes. Each recipe is a known fix for one or
more of them, with stack-specific detail for GitHub Actions, CDK, Docker,
Python, Node and Go.

| Waste | What it looks like | Recipes |
|---|---|---|
| W1 No-op not a no-op | An unchanged deploy still updates stacks or builds images. | R2 Turn off framework version reporting; R3 Keep rotating secrets out of image asset hashes; R4 Isolate images that share a build context |
| W2 Unpinned toolchain | Floating versions let the same tree build differently. | R1 Pin the toolchain through version files |
| W3 Emulated builds | Images build under CPU emulation. | R6 Build once, outside deploy; promote by digest |
| W4 Uncached work | Unchanged installs or layers are rebuilt. | R6; R11 A cache that never warms |
| W5 Serial where it could be parallel | Independent work runs in a line, or repeats itself. | R5 Find the duplicate before parallelizing tests |
| W6 Re-verification | The same checks run twice on the same tree. | R10 Reuse proven checks without dropping deploy-specific gates |
| W7 Chain stalls | An automated hand-off fails with no visible outcome. | R7 Automated hand-offs never stall silently |
| W8 Silent runner-plane failure | Jobs queue because no runner can take them. | R8 Keep runners current and make a refused plane loud |
| W9 Rebuilt artifact | The same inputs are built again in a later stage. | R6 |
| W10 Ignored advance notice | Platform warnings go unread until jobs break. | R9 Read and act on advance notices |

Read them with `muda standard`, `muda standard W4`, `muda recipes` and
`muda recipe R11 --stack go`.

## How proof works

- **Every number names its evidence:** a run, job, step, log line or file.
  Missing data is `unavailable`, with a reason. It is never read as zero.
- **A finding is `measured` or `suspected`.** Measured means the evidence shows
  the waste. Suspected means it is likely but not shown. A suspected finding can
  only be fixed as a labelled experiment, with pass criteria agreed first.
- **Gates are checked before and after.** `muda compare --gates` compares the
  recorded gate inventory with the pipeline after the change:
  - `VERIFIED`: the gates are the same or stronger, and that is proven.
  - `EXEMPTED`: every gap is covered by an exact exemption a person approved.
    That is acceptance, not proof.
  - `UNVERIFIED`: proof is missing. The finding is not fixed.
  - `WEAKENED`: a gate was removed or loosened. The finding is not fixed.
- **Decisions live in `.muda/`** in your repository: the baseline, findings,
  exemptions and receipts. They reach the default branch through pull requests,
  merged by you or by the agent once you say so.

## What muda never does

- It never changes repository settings, branch protection or rulesets. It only reads them.
- The fixes are made by your coding agent, guided by the skills: it edits your workflows on a branch and opens a pull request, only after you approve the plan. The `muda` binary itself only reads your pipeline and writes its record in `.muda/`.
- It merges only when you say yes to that pull request, with every required check green.

## Learn more

- [muda.tools](https://muda.tools)
- [docs/principles.md](docs/principles.md): the rules behind the standard.
- Validation receipts:
  [stage 1, a blind replay](docs/validation/2026-09-30-stage-1-replay.md) and
  [stages 2 and 3, the full loop and a public repository](docs/validation/2026-10-02-stages-2-3.md).
- [CONTRIBUTING.md](CONTRIBUTING.md)

## License

MIT. See [LICENSE](LICENSE).
