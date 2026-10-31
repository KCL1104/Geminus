# Investigation Planner tools — prompt_template_version: investigator-0.1.0

## propose_what_if
Scenario mode. Ask for the next what-if on the investigation's model: describe one change in words (idea: one paragraph without line breaks, at most 2000 characters), or set from_suggestion to an earlier explanation's suggested experiment (its analysis_id and index; the server then writes the request from that suggestion) with idea saying why. The proposal pipeline turns it into a reviewed scenario proposal; its result says whether it is ready, needs input, is invalid or failed. At most 3 what-ifs; refused with LIMIT_REACHED after that, BASE_MOVED when the model has a newer version.

## save_scenario
Scenario mode. Save a ready what-if of this investigation as a scenario, by its proposal_id. The server checks it again against the model first. Refused with PROPOSAL_NOT_READY when it is not ready, PROPOSAL_DECIDED when a person already approved or rejected it, BASE_MOVED or RECHECK_FAILED.

## run_what_if
Scenario mode. Run a scenario this investigation saved, by its scenario_id, against the baseline, with the investigation's frozen run settings (the baseline runs once). The result is the server's comparison of the two runs on the objective and a few other metrics. At most 4 runs, the baseline included; refused with LIMIT_REACHED, ALREADY_RUN or NOT_IN_INVESTIGATION.

## explain_results
Scenario mode. Ask the analyst to explain a completed what-if run of this investigation against its baseline, by its run_id, with an optional question (one paragraph, at most 500 characters, or null). Every finding the result shows passed the server's check against the runs; its suggested experiments can seed propose_what_if. At most 4 explanations; refused with RUN_NOT_COMPLETED, LIMIT_REACHED or NOT_IN_INVESTIGATION.

## propose_model_edit
Edit mode. Ask for the one change to the model this investigation may make: describe it in words (idea: one paragraph without line breaks, at most 2000 characters). The proposal pipeline turns it into a reviewed edit proposal; its result says whether it is ready, needs input, is invalid or failed.

## write_to_draft
Edit mode. Write a ready edit of this investigation into the model's draft, by its proposal_id, only when the draft holds none of a person's changes. A person reviews and publishes the draft; the investigation then ends. Refused with DRAFT_CHANGED, PROPOSAL_NOT_READY, PROPOSAL_DECIDED, BASE_MOVED or RECHECK_FAILED.

## finish
Both modes; always offered. End the investigation with your outcome: improvement_found, no_improvement, out_of_ideas or needs_person, and best_scenario_id, the scenario you think did best (or null). Both are shown to the person only as your claim, beside the server's own ranking and closing line.
