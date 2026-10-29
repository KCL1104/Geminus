# Hackathon plan: Meta Global AI Developer Hackathon (H-029)

Basis: H-029 (2026-10-26; the owner has been accepted into the Indie track and chose the scope and process below), amended by H-030 and H-031: **Railway only; do not connect Meta yet; validate and demo with the Claude API first; use Claude's image input for photo recognition first**. H-032: the owner has provided a Railway token and authorized the agent to deploy the demo (the owner fills in the secrets in Railway). **H-033: the owner has added the Meta (Muse) key, and Meta is restored: Muse Spark for natural-language edits, SAM 3.1 + Muse Spark for photo recognition, and Claude as a switchable fallback.** This file is the work plan for the competition period. It does not change the P0 scope or any approved decision. After the competition, the H-003 review process resumes, and deferred tasks proceed per `planning/tasks.json`.

## Competition

- Online, starts 2026-10-26; **submission deadline 2026-11-04 21:00 ET (2026-11-05 02:00 UTC)**; judging 11-04 to 11-08; results announced 11-09.
- Scoring: Impact 30%, Use of Meta AI Tooling 25%, Technical Depth & Execution 20%, Demo 25% (a working demo and a video).
- Theme: "Push the Frontier" names manufacturing and "understand and control a physical system"; Geminus's digital twin of an electronics assembly/test line fits the theme directly.
- Key rules (Official Rules, retrieved 2026-10-26): the work must be the entrant's original work, and the rights belong to the entrant; open-source components must follow their licenses; after submission the entrant keeps ownership but grants the organizers a perpetual, irrevocable, royalty-free, non-exclusive license to use it; content shared during the event is not treated as confidential. The rules do not say "must start from scratch"; the submission states what was built during the competition.

## Demo contents (H-029 (2))

1. Electronics line template (SMT, AOI, ICT, burn-in batch, packaging) and synthetic data.
2. **Photo → model**: upload a line photo or floor plan; SAM 3.1 finds machines and conveyors, and Muse Spark identifies stations and flow direction (H-033; Claude's image input is the fallback), producing a model proposal; the server validates it and computes risk; it takes effect only after human approval.
3. **2D editor**: view and adjust the model on the canvas.
4. **Propose edits in natural language**: for example "change burn-in to 48 boards per batch"; Muse Spark generates a proposal (H-033; Claude is the fallback), and the screen shows the diff, the AI's assumptions and the server-computed risk side by side.
5. **Simulation and comparison**: run the simulation; a Marey chart compares baseline and scenario, plus the main metrics.

Rules that do not change: AI only proposes; it cannot approve or change the model directly; risk and validation are computed by the server; tenant isolation; return needs_input when data is insufficient; never fabricate any result or number.

## Slices and dependencies

| Slice | Task | Depends on | Planned completion (UTC) |
|---|---|---|---|
| Meta provider (Muse Spark) | T-055 (restored by H-033) | T-024, T-025, T-053 | 10-28 |
| Electronics line template and metrics | T-051 slice | T-050 | 10-28 |
| Synthetic line data | T-052 slice | T-051 | 10-29 |
| 2D editor | T-015 slice | T-045 | 10-30 |
| Container and deployment (Railway only, H-031, H-032) | T-039 slice | Railway token (available), Auth0 tenant | 10-31 |
| Photo → model proposal (SAM 3.1 + Muse Spark, H-033) | T-056 | T-055, T-051 | 10-31 |
| Natural-language edits and the proposal review screen | T-031 slice | T-053, T-015 slice | 11-01 |
| Simulation and Marey comparison screen | T-032 slice | T-031 slice | 11-02 |
| Deployment, demo script, video, submission | T-057 | All of the above | 11-04 |

Code freezes at 11-03 23:59 UTC; 11-04 is only for deployment checks, the video and the submission text.

## Process (H-029 (4), during the competition)

- Each slice writes tests first, then the implementation; security rules, negative tests and assertions stay as before, and no skips are added.
- Each slice gets one round of independent review; it is complete once High/Medium findings are fixed and verified by the coordinator; Low findings are recorded as post-competition follow-ups.
- Deferred until after the competition: the remaining P0 tasks, the seventh decision page (D-096, D-097, D-110–D-116), and T-053's Anthropic live run (answer D-116 before running it).

## What the owner needs to do

| When | Item |
|---|---|
| Done | Add the Meta (Muse) key to the environment variables (H-033) |
| Done | Add the Railway token to the environment variables (H-032) |
| Before 10-29 | Register for Auth0, create a tenant (region: US recommended) and your own login user, and share its `user_id`; the agent configures the rest at deployment time through device login (H-034; on deployment day, approve in the browser) |
| Before 10-31 | Fill in the Meta key and the Anthropic key (fallback) in Railway's variables and seal them right after filling them in (H-036); the Auth0 Client Secret is written directly by the deployment script (H-034) (the agent provides the list of names; Railway generates the session secret; secrets do not go into the repo, and the agent does not read the values) |
| 11-04 | Watch the demo video (record a narration if needed), and submit on Cerebral Valley as the Indie track Representative |

## Data and spend

- Claude API (H-031): send only synthetic data, demo images and text the user types; requests do not specify inference_geo (D-116 option A); logs do not store raw prompts or responses.
- Spend keeps T-053's in-code caps and switches (live runs only on independently reviewed code, H-025).
- Meta (H-033): Standard mode (official pricing page: Standard mode content is not used for training); accepted entrants get USD 150 of Meta API credit (10 days); official prices (2026-10-26): `muse-spark-1.3` USD 1.25 per million input tokens and USD 4.25 per million output tokens, `sam-3.1` USD 2.50 per thousand images. "Use of Meta AI Tooling" is 25% of the score; Meta live calls run only on independently reviewed T-055 code (as with H-025).
- Photo proposal reservation (H-037): for a request with an image, first get the input token count with count_tokens and reserve that plus 10%; reserving by the context window costs about USD 4.04 each time, so under the organization's USD 5 daily cap only one would fit per day.
