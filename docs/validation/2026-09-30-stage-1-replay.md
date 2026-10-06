# Validation stage 1: blind replay of a known period

**Result: pass** (run 2). The session found all five target wastes, each with cited evidence, and made no false positives.

Repo-specific detail is kept in the maintainers' private records. This receipt contains no project facts.

## What stage 1 tests

- **Setup:** one real repository whose delivery wastes had already been audited by hand, replayed over a closed 30-day window, read-only.
- **Agents:** a fresh agent session received only the installed muda skills and an opening request. A second blind agent played the repository owner at each checkpoint. Neither saw the answer key.
- **Pass bar:** the session must find five named wastes (W1, W3, W6, W7, W9), each citing a run, job, step, log line or file.

## Harness

- **Clone:** the checkout ends at the window's last commit. It has a single local branch, no remote-tracking refs and no fetch refspec.
- **Wrappers:** a `gh` wrapper allows read subcommands only. It also refuses listings that expose later history: commits, compare, branches, tags, releases and PR lists. A `git` wrapper refuses fetch, pull and ls-remote.
- **Recording:** the session commits `.muda/` locally and stops after diagnosis.
- **Audit:** afterwards, the transcript is checked for reads outside the clone and the skill files, writes outside the clone, and pushes.

## Runs

### Run 1: fail (4 of 5)

- **Results:** W3, W9 and W7 were measured. W6 was suspected, with an honest note that equivalence couldn't be proven. W1 was partial: the session saw the image rebuilt in every sampled deploy but put that time down to W3.
- **Root cause:** muda-diagnose told the agent to read *one* recipe per sink. After R6 explained the top sink, the session never ran R3's Detect, so the rotating build argument went unseen.
- **Fix:**
  - Read every recipe listed for a sink and run each Detect. One sink can carry several wastes.
  - R2 and R3 got concrete Detect steps. Measured needs a rebuild across an unchanged-input diff (R3), or a metadata-only template difference (R2).
  - Example greps are now case-insensitive and broad.
  - Pressure-tested on a fabricated repository.

### Run 2a: discarded (harness leak)

- **Leak:** the clone kept the default branch's later history. The session compared the replay commit with today's branch and read commit subjects naming the later fixes. A reviewer in the past could not have had that hint.
- **Result:** the run was discarded. The clone recipe above, plus the wrappers, came out of it.

### Run 2: pass (5 of 5)

| Waste | Label | Evidence the session cited |
|---|---|---|
| W1 work in unchanged deploys | measured, approved | 5 consecutive deploys with no change to the image's inputs, each with a new asset ID. The IaC passes a rotating registry token as an image build argument. |
| W3 emulated builds | measured, approved | 10 of 10 sampled deploys build arm64 images under QEMU on x86, with the install step taking most of the deploy step. |
| W9 rebuilt per stage | measured, approved | Paired dev→prod deploys of the same tree. Prod rebuilt an image with the same content hash. |
| W7 hand-off stalls | measured, approved | All bump failures classified by their error line. 4 were silent merge-timing stalls, later merged by hand. |
| W6 re-verification | suspected | The deploy re-runs checks on a tree already verified. The session honestly declined to claim CI-to-deploy reuse: the CPU architecture and Node version differ. |

**Extra true findings:** an ignored runner-retirement notice (W10), Node version drift between pipelines (W2), and serial tests (W5, suspected).

**Harness checks:**
- The session made two attempts to see later history. Both were blocked.
- It read nothing outside the clone, wrote nothing outside it, and pushed nothing.

## Follow-ups before stage 2

1. **Report every listed recipe's Detect outcome.** Run 2 read R2 but never said whether its Detect ran. A second W1 case was missed: every stack updated after a runtime patch-version change. The diagnose checkpoint should list each listed recipe with its outcome (ran, or does not apply, and why).
2. **Let replays record the past.** `record baseline` accepts gates and scan only from the default branch as it is today. In a replay, that shows the agent the later workflows, including jobs that hint at fixes. Allow `--ref` reads when the window has already closed.
3. **Document the replay clone.** Write down the clone recipe and the wrappers as the standard for replays, so later stages can't leak.
4. **Carried over from pre-validation:**
   - an expression-pin over-block of drift-fix proof;
   - a workflow turned into a symlink reads as removed;
   - re-run the "re-measure to be safe" scenario;
   - decide how a null `mergedAt` is handled;
   - store the full SHA-256 in exemptions before any public release.

## Regression after the follow-ups

**Result: pass.** After the follow-ups shipped, stage 1 was replayed on a fresh harness built from the replay harness standard, which is kept with the maintainers' private records. Every target waste that occurred inside the window was found, with evidence, and there were no false positives.

| Waste | Label | Evidence the session cited |
|---|---|---|
| W1 work in unchanged deploys | measured, approved | 58 of 58 consecutive deploys whose image inputs did not change still rebuilt one image. The cause is a per-run registry token passed as an image build argument. The session correctly excluded a sibling image whose asset construction already ignores build arguments. |
| W9 rebuilt per stage | measured, approved | Dev and prod deploys produced the same image asset hash, and prod rebuilt it anyway. |
| W7 hand-off stalls | measured, approved | All 14 bump failures classified by their error line. 4 were merge races after green checks. |
| W3 emulated builds | suspected | QEMU use is measured. The saving stays suspected because there is no native build timing to compare. |
| W6 re-verification | suspected | Matching step names on the same tree, but CI and deploy run on different CPU architectures, so the checks may not be equivalent. |

**What the follow-ups changed, as seen in the run:**
- **Historical baseline:** the baseline was recorded at the window's last commit. Gates and scan were read with `--ref`, and the settings were labelled as read today.
- **Bounded reads:** every measure, scan and notices read was bounded with `--since`/`--until`.
- **Recipe outcomes:** the diagnose checkpoint listed an outcome for every recipe matched to each sink.
- **Gate inventory:** an owner request to add gates by hand was recorded as the owner's attributed statement, not as an edit.

**Answer-key correction:**
- **Second W1 case:** the stage-1 key also expected "every stack updates after a runtime patch-version change". The deploys showing it ran after the window closed, so it was not a waste of this window. The regression's "not shown" for that recipe is correct, and run 2's "miss" was not a miss.
- **Sibling image:** the key also wrongly counted the sibling image under W1. Its rebuilds are W3 and W9.

**Exposure, caused by the controller:** the controller reused an owner reply from run 2 that asked the session to "call out where today's default branch differs". The session then read today's gates and scan once each. Its two further attempts to see later history were refused by the wrappers. Every finding's evidence is from logs and files inside the window. The harness standard now forbids relay text that invites comparison with today, and forbids reusing an earlier replay's owner replies.

**Harness:**
- **Lost first attempt:** the first regression session was lost when the controlling session restarted. It was discarded and rerun from a clean clone.
- **Audit:** the run read nothing outside the clone and the skill files, apart from the agent runtime's own spill file for long command output. It wrote nothing outside the clone and pushed nothing.
