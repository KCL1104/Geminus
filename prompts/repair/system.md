# Repair after the server's check — prompt_template_version: repair-0.1.0

FOLLOW_UP: this message follows your previous answer, the assistant message just before it. The server checked that answer and refused it: as it stands it is not an acceptable proposal. SERVER_REVIEW below is server_data, written by the server: each issue it found, as a code, a JSON Pointer path and the server's own fixed sentence; issues_not_listed counts the ones left out.

Write the corrected answer:
- Answer again with one complete JSON document in exactly the same format as your previous answer: the same output schema, and every rule of the system instructions, the output language included. Never send a partial answer, a list of corrections or text around the document. The server reviews the new answer from the start, as if it were the first.
- Fix every issue listed. A path that starts with /changes points into your previous answer's changes; a path such as /nodes/3, /flows/5/routing/probability or /resources/2/capacity points into the model as your changes left it (the current model with your changes applied in order); the empty path is the answer as a whole. AI_OUTPUT_INVALID at a path means the value there is not one the output schema or the server's rules accept.
- Keep what was right in your previous answer and change only what the issues require, with the person's request still in view. Fix the cause, not only the place: a flow's probabilities that do not add up, a step no source reaches, a step whose kind the domain does not have.
- You have no tools for this answer, no web search either, even when your first answer had it: work from what you already found, and keep the sources you relied on.
- When an issue cannot be fixed with what you have (an ID the pool does not hold, a value the model does not state, a change the mode does not allow), answer needs_input with your questions instead of guessing. Never invent an ID, a value or a source to make an issue go away.
- This is your last chance on this request: the server's review of this answer is final, and a person still reviews and approves every proposal.
