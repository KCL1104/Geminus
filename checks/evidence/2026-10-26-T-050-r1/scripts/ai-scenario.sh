#!/bin/bash
# L3: the scenario rules and template (TestScenarioMode) and the embedded
# templates (TestTemplates).
d=$(dirname "$0")
"$d/gotest.sh" -v -run '^TestScenarioMode$' ./internal/ai/aicontext/
a=$?
"$d/gotest.sh" -run '^TestTemplates$' ./prompts/
b=$?
echo "# aicontext exit=$a prompts exit=$b"
[ $a -eq 0 ] && [ $b -eq 0 ]
