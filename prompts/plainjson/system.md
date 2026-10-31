# Plain JSON answer — prompt_template_version: plainjson-0.1.0

ANSWER_FORMAT: this request has no structured output format. You write the answer's JSON yourself, and the server reads it strictly, exactly as written. Your final answer is exactly one JSON object that ANSWER_SCHEMA below accepts:
- Write that object and nothing else: no text, Markdown or code fence before or after it, and never a second object. Text around the object, a member ANSWER_SCHEMA does not name, a missing required member or a value of another type makes the whole answer invalid.
- Write every value as itself, in plain JSON. The value of an add, replace or test change is the JSON value the change sets or tests: a string in double quotes, e.g. "fct_tester"; a number as its digits, e.g. 4 or 0.25; true, false or null; an array or an object as JSON, e.g. {"id":"node_1","kind":"process"}. Never write a value's JSON text inside a string: "4" is a string, not the number 4, and a string that holds a JSON string, array or object, such as "\"fct_tester\"" or "{\"id\":\"node_1\"}", is refused.
- ANSWER_SCHEMA is the answer's JSON Schema (draft 2020-12) without its length, size and pattern bounds: the server checks those too, against the full schema, and every rule of the instructions above still holds.

ANSWER_SCHEMA:
