# T-053 schema follow-up: independent review, round 1 (H-003, H-029 hackathon mode)

Reviewer: an independent agent. I did not write the fix. Date: 2026-10-26. Tree: `09ed10e`, reviewed as
`git diff 9cf8e9a 09ed10e` (commits 51dbdb2, b188c51, f930f50, 09ed10e). Every log in this directory ends with
its exit code. No request went to the provider. I did not read or print `GEMINUS_ANTHROPIC_API_KEY_A`/`_B` or
`ANTHROPIC_API_KEY`. Every HTTP probe used the package's local httptest server.

The probes (`probe_internal_test.go.txt`, `probe_test.go.txt`) ran from inside the worktree's
`internal/ai/provider/anthropic` package. I then moved them here, and `git status` shows only this directory.
Mutants, schema mutants and the fuzz run only in `git archive HEAD` copies in my scratchpad, outside the
worktree.

## Verdict: CHANGES REQUESTED

There is one Medium finding (M1) and no High finding. Everything else holds:

- the projection and its guard;
- the closed error-type list;
- spend and gating;
- the other decode rules.

M1 is narrow, but it sits exactly on the path this review gates: from the model's answer to a ready proposal.

## Findings

### M1 (Medium): an answer whose encoded values do not decode can still become ready

**Where.** `internal/ai/provider/anthropic/encoded.go:52-55`:

```go
out, ch, err := rewrite(doc, loc.Steps, "", &issues)
if err != nil {
	return text, nil // cannot happen for a valid document; the client's check decides
}
```

**The path is reachable.** `decodeText` accepts a JSON text nested up to jsontext's limit of 10000 levels
(`IsValid`, encoded.go:146). Decode then puts that value back at `/changes/i/value`, three levels down, and
re-marshals the item, the array and the root. Inner nesting of 9998, 9999 or 10000 makes that document deeper
than the encoder takes, so `json.Marshal` fails. Decode then returns the **original answer with no issue**:

- every encoded value of the answer stays the string the model wrote;
- every issue already found for the other values is dropped;
- the full schema accepts the result, because the canonical `value` is `{}`.

**Evidence.**

- `probes-full.log` lines 44-49 (P2): depths 9998, 9999 and 10000 come back `UNCHANGED, no issue`, and the full
  schema reports `schema-valid`. Depth 10001 is refused, which is the only depth `TestDecodeRefuses` covers.
- `probes-full.log` lines 63-67 (Q1), end to end over HTTP and the live eval's client and server review, on
  MOD-002's base. The answer is: `test /nodes/1/id "code_review"`, then `add /nodes/1/x <9998-deep>`, then
  `remove /nodes/1/x`, then `replace /name "Renamed by a raw string"`. Two of its values (`code_review`,
  `Renamed by a raw string`) are not JSON texts.
  - With a 100-deep value it is `invalid_output` with two issues, which is correct.
  - With the 9998-deep value the client returns it, and the review makes it **`ready`**: `server_risk low`, a
    patch hash, every gate 0. The values are taken as raw strings.
- `fuzz-seeded.log`: the fuzz invariant ("an answer that passes the full schema has every add, replace and test
  value decoded from a JSON text") fails on that seed. The 120 s fuzz run (`fuzz-120s.log`, 1.7 M executions)
  found nothing else.
- `mutants.log`: R14 marks this behaviour as equivalent to HEAD, and every test passes.

**Impact.** This breaks the one rule the projection adds. The rule is stated in AI_SPEC §2 and in the
report's 修正 2: "該位置不是這樣的字串…回答即 `invalid_output`…不會有 ready 的 proposal". It is also stated as
"nesting 在 jsontext 的 10000 層之內" (checks/T-053_VALIDATION.md:512).

Authority does not widen:

- the server still validates the candidate, its patch policy and its pack rules;
- the server computes the risk;
- a person still approves.

Spend, keys and the record are unaffected. Triggering M1 needs about 20 KB of brackets inside the 64 KiB and
8192-token bounds. A benign model will not write that, but a prompt-injected one could.

**Suggested fix.**

1. **Fail closed.** On any error from `rewrite`, return no answer and an `AI_OUTPUT_INVALID` issue. F1 in
   `mutants.log` is that change (root path, fixed message), and it passes every existing test in
   `internal/ai/provider/anthropic` and `internal/aievals` (`FIXPASS`).
2. **Bound the nesting of a JSON text, with headroom.** For example, read the text with a `jsontext.Decoder`
   and refuse `StackDepth()` above a small bound. The IR's own containers stop at `MaxContainerDepth = 8`, so
   64 is generous. This makes the encoder error impossible rather than merely handled.
3. **Add tests:**
   - Decode at depths 9997-10000;
   - an HTTP and `liveRun` case like Q1 that must end `invalid`;
   - a negative assertion that issues found before an internal error are not lost.
4. **Correct the report and AI_SPEC.** Fix the AI_SPEC §2 sentence and the report row of `TestDecodeRefuses`
   ("超過 nesting 上限").

### L1 (Low): safety properties of the projection and the guard that no test holds

Four of my mutants survive (`mutants.log`). In each case the current code is right, but no test would notice a
regression:

- **R2:** a const member that is not required in every alternative can serve as the discriminator.
  `projection.go` `discriminator` requires it to be required, and that is what keeps an alternative from being
  matched without its const member. Without that check, an encoded string could reach the full schema undecoded,
  the same class of failure as M1.
  - The schema mutant X9 (`op` no longer required in `remove`) is still killed today, through the projector's
    refusal.
  - No unit test of `discriminator` exists. Add a `TestProjectionRefuses` case with an optional const member.
- **R8:** a description-only projection would count as `typed`, so a schema left with a description and only
  dropped constraints would be sent as an empty schema. `TestProjectionRefuses`'s "only dropped constraints" case
  has no description. Add one with a description. The guard would still catch it on the real schemas.
- **R10:** the guard does not count type arrays (`"type": ["string","null"]`) as union parameters.
  `TestTheSubsetGuard`'s "too many unions" case uses `anyOf` only. Add a type-array case.
- **R1:** the 20-issue cap of Decode (`maxIssues`) is untested. The client does not cap Decode's issues again,
  so without the cap one answer could carry an issue per change. Add a case with more than 20 bad values.

R3 (`inner.Compact()` removed) also survives, but it is effectively equivalent: re-marshaling the rewritten
item compacts the raw value anyway. That is Info only.

### L2 (Low): stale documentation of the projection's acceptance and of the decode step

- **`docs/SECURITY.md:96` (§7 "Output").** It still describes the output path as schema check, then strict
  decode. It does not mention that the adapter now re-parses model strings as JSON at the three encoded places
  before that check, under the strictness and bounds of `encoded.go`.
- **`docs/SECURITY.md:97`, the report's NOT RUN and `projection.go`/`anthropic.go` comments.** These still say
  that acceptance of the projection by the API is NOT RUN. The brief says the coordinator's free `count_tokens`
  call returned 200 (input_tokens 1682) on 2026-10-26, but that result is recorded nowhere in the repository
  (`grep -rn 1682 checks docs` finds nothing).
- **Fix.** Record that result with its evidence in `checks/T-053_VALIDATION.md`: date, model, endpoint,
  status, input_tokens, and which projection, for example the sha256 of `projected-proposal-output.txt`. Then
  move the item out of NOT RUN.
- **What stays NOT RUN.** Only that run is evidence of acceptance. Whether the model writes the JSON texts can
  only be shown by the live smoke and eval.

### Info

- **I1 Enum and const casing.** The official structured-outputs page ("Invalid outputs" → "Enum value casing",
  retrieved 2026-10-26) says capitalization of `enum`/`const` values is not guaranteed.
  - An answer with `"status":"Proposed"` or `"op":"Add"` is not decoded, and the full schema refuses it (P1:
    `unchanged` then `schema-invalid`).
  - That is the safe side. Still, expect some `AI_OUTPUT_INVALID` in the live eval for this reason, and read
    the issue paths (`/status`, `/changes/i/op`) before blaming the encoding.
- **I2 Structural matching holds (P1).** Each of these was tried:
  - JSON-looking strings in the summary, assumptions, effects and path stay strings;
  - `remove` with a value, `needs_input` with changes, a missing or numeric `op`, nested `changes` arrays and
    `changes` as an object are all left alone and refused by the full schema;
  - escaped discriminators (`proposed`, `add`) and escaped member names (`value`) are decoded,
    consistently with the full schema;
  - root and item duplicate names (also after unescaping) and trailing data after the answer reach the client
    unchanged, and its parse refuses them;
  - trailing JSON white space is accepted;
  - BOM, NBSP, NUL, comments, NaN, a leading zero, single quotes, a raw LF inside the inner string, lone
    surrogates and duplicates inside the value are each `INVALID` at the right pointer, with a fixed message;
  - double encoding decodes exactly once, to a string;
  - 1000-digit precision and `-0E+00` are kept as written; `1e999999` is refused by the full schema;
  - `\u0000` inside a decoded string passes Decode and the full schema (P1). From reading the code, not from a
    probe, the review's NUL check (`review.go` `outputIssues` → `nulAt`) then refuses it, because the escape is
    kept as written.
- **I3 Bounded cost (P3).** Decode is linear:
  - 1000 changes: 47 KB in 23 ms, 8 MB allocated;
  - 10 000 changes: 490 KB in 152 ms, 83 MB allocated (transient, about 170× the answer);
  - at most 20 issues;
  - no panic in 1.7 M fuzz executions.

  `max_tokens` bounds the answer (default 8192, cap 32000).
- **I4 The guard matches the page.** The structured-outputs page (retrieved 2026-10-26) lists the supported
  features, the unsupported ones, and the explicit limits of 24 optional and 16 union parameters (20 strict
  tools does not apply here). The guard matches that list and is stricter on `pattern` and `title`.
  - The page also has internal grammar-size limits ("Schema is too complex for compilation") and a 180 s
    compile timeout, which no offline guard can check.
  - Ten schema mutants (`schema-mutants.log`): `{}` reintroduced in a member, an `anyOf` alternative, array
    items or `additionalProperties`; open objects; a dropped-only value; a lost discriminator. All 9 non-design
    ones fail offline. X8 (`{}` replaced by `true`) is encoded the same, by design.
- **I5 Error types are a closed list.** The errors page (retrieved 2026-10-26) lists 11 types (400
  `invalid_request_error` … 529 `overloaded_error`) and says `type` values may grow. `errorTypes` matches it
  exactly; any other value becomes `other`.
  - Q3 tried a type with a newline, a key-shaped type (`sk-ant-api03-LEAKLEAK`, my own fake string), a path,
    a trailing space and an empty type. Each gave `other`, with no message, key or type text in `Error()` or the
    eval report.
  - The worker's keys are registered (`provider_status` number, `provider_error_type` token).
- **I6 Spend and gating show no regression.** The diff does not touch the meter, the ledger, the record, the
  config or the migrations (`git diff --stat` is empty for those paths).
  - Of the round-1 harness's 98 mutants, I ran the 77 that need no database. 76 were killed. K11 no longer
    applied because `main.go` gained `providerErrors()`; I brought it to the new code as K11a, and it was
    killed (`mutants-rerun.log`).
  - The 21 DB mutants are NOT RUN; their code is unchanged.
  - A Decode failure keeps the usage and settles (R12 killed).
- **I7 Request diagnosability (R13 killed).** 429 and 5xx carry their type as well, not only 4xx.
- **I8 The fix's own harness.** S1-S17 (23 mutants) were re-run in a copy, with hash restoration instead of
  `git status`: all killed.

## Commands and exit codes

| Command | Exit | Log |
|---|---:|---|
| `go test -count=1` on `./internal/ai/...`, `./internal/aievals/...`, `./internal/config/...`, `./internal/observability/...`, `./internal/proposals/...`, `./cmd/ai-eval-live/...`, `./cmd/worker/...` | 0 | `test-changed.log` |
| `go test -race -count=1`, same packages | 0 | `race-changed.log` (generator and worker DB tests skip without a database) |
| `gofmt -l` and `go vet`, changed packages | 0, 0 | `vet-fmt.log` |
| `make spec-check PYTHON=/home/user/Geminus/.venv/bin/python` | 0 | `spec-check.log` |
| Probes P1-P4, Q1-Q3 (`go test -count=1 -run TestReviewer -v`) | 0 | `probes-full.log` (logging probes: the outcomes above are in the log lines) |
| Fuzz seeds with M1's seed (`REVIEWER_DEPTH_SEED=1`) | 1 (expected: M1) | `fuzz-seeded.log` |
| `go test -fuzz FuzzReviewerDecode -fuzztime 120s` | 0 | `fuzz-120s.log` |
| `reviewer_mutants.py <copy> <worktree> R s r1`: 115 mutants | 1 | `mutants.log`: 106 KILLED, 5 SURVIVED (R1, R2, R3, R8, R10), 1 EQUIVALENT (R14 = M1), 1 FIXPASS (F1), 1 ERROR (K11, pattern changed), 1 INVALID (R9, my mutant did not build) |
| `reviewer_mutants.py <copy> <worktree> K R9` | 0 | `mutants-rerun.log`: K11a and R9 (fixed form) KILLED |
| `schema_mutants.py <copy>`: 10 schema mutants | 0 | `schema-mutants.log`: 9 KILLED, 1 BY-DESIGN |
| `grep -rlE 'sk-ant-[a-z0-9]' checks/evidence/2026-10-26-T-053-schema-r1/` | 0 | the only matches are my fake error type `sk-ant-api03-LEAKLEAK` in `probe_test.go.txt` and `probes-full.log`; no real key |

**Official pages read on 2026-10-26:**

- https://platform.claude.com/docs/en/build-with-claude/structured-outputs ("JSON Schema limitations",
  "Schema complexity limits", "Invalid outputs");
- https://platform.claude.com/docs/en/api/errors ("HTTP errors", "Error shapes").

## NOT RUN

- **Live calls of any kind:** smoke, eval and `count_tokens`. They are not mine to make.
- **`make integration-test`, `make backend-test` (whole tree) and the 21 DB mutants.** The diff does not touch
  DB code. The coordinator's `integration.log` and `backend-test.log` exist, with exit 0 in `exit-codes.txt`.
- **`make frontend-test` and `make e2e-test`.** There is no UI, contract or TS change.
- **govulncheck and `make architecture-check`.** The coordinator's logs exist, and no dependency or import
  changed beyond the diff.

## May the live smoke and eval be re-run on this code?

**Not on `09ed10e` as it is.** H-029 requires High and Medium findings to be fixed and verified by the
coordinator first. After M1 is fixed (the fail-closed change F1 plus a nesting bound and its tests) and the
coordinator has verified it, the smoke and then the eval may be re-run under H-025 without another review round.

M1 does not touch spend, keys, the record or the gates. The round-2 preconditions still apply:

- one run at a time in the main checkout, committing `checks/ai-evals/` after each;
- default caps, with no `-dir`;
- the smoke first, and the eval only if it gives no warning;
- the eval's cap set to 10 minus the smoke's committed amount;
- `grep -r 'sk-ant-' checks/ai-evals` afterwards;
- stop on any `over_reservation` or `inference_geo_unknown` warning.

L1 and L2 can follow, though L2's `count_tokens` record should be written before the smoke so that the run's
evidence is complete.
