#!/usr/bin/env python3
"""T-053 I5 follow-up: the negative cases of scripts/check_deploy.py's new
rules. Each case changes deploy/railway/variables.json (or the Go list it
reads) in memory only, by replacing what pathlib.Path.read_text returns for
that one file, and expects railway() to fail with the rule's message.
Nothing on disk changes; no network. Run from the repository root:
    python -I checks/evidence/2026-10-26-T-055-apiavail/check_deploy_negatives.py
"""
import copy
import importlib.util
import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[3]
spec = importlib.util.spec_from_file_location("check_deploy", ROOT / "scripts/check_deploy.py")
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)

real_read = pathlib.Path.read_text
VARS = ROOT / "deploy/railway/variables.json"
APIAI = ROOT / "internal/config/apiai.go"
base = json.loads(real_read(VARS, encoding="utf-8"))


def run(vars_doc=None, apiai=None):
    def read_text(self, *a, **k):
        if vars_doc is not None and self.resolve() == VARS:
            return json.dumps(vars_doc)
        if apiai is not None and self.resolve() == APIAI:
            return apiai
        return real_read(self, *a, **k)
    pathlib.Path.read_text = read_text
    try:
        mod.railway(mod.code_variables())
        return None
    except AssertionError as e:
        return str(e)
    finally:
        pathlib.Path.read_text = real_read


def edit(fn):
    d = copy.deepcopy(base)
    fn(d["services"])
    return d


def remove(lst, x):
    lst.remove(x)


cases = {
    "the API does not forbid MODEL_API_KEY": (edit(lambda s: remove(s["api"]["forbidden"], "MODEL_API_KEY")), None,
                                              "must forbid every AI key variable it refuses"),
    "the API does not forbid ANTHROPIC_AUTH_TOKEN": (edit(lambda s: remove(s["api"]["forbidden"], "ANTHROPIC_AUTH_TOKEN")), None,
                                                     "must forbid every AI key variable it refuses"),
    "the API holds the Meta key": (edit(lambda s: (remove(s["api"]["forbidden"], "GEMINUS_MUSE_API_KEY"),
                                                   s["api"]["variables"].__setitem__("GEMINUS_MUSE_API_KEY", {"secret": True, "source": "owner", "required": False}))),
                                   None, "must forbid every AI key variable it refuses"),
    "the web does not forbid the Meta key": (edit(lambda s: remove(s["web"]["forbidden"], "GEMINUS_MUSE_API_KEY")), None,
                                             "the web must forbid the live adapters' keys"),
    "the API has no AI_MODEL": (edit(lambda s: s["api"]["variables"].pop("AI_MODEL")), None,
                                "AI_MODEL must be declared on the API and on the worker"),
    "the worker has no AI_POLICY_APPROVAL": (edit(lambda s: s["worker"]["variables"].pop("AI_POLICY_APPROVAL")), None,
                                             "AI_POLICY_APPROVAL must be declared on the API and on the worker"),
    "the API's switch set by the deployer": (edit(lambda s: s["api"]["variables"].__setitem__("GEMINUS_AI_LIVE", {"value": "on"})), None,
                                             "the API's GEMINUS_AI_LIVE must be declared as the worker's"),
    "the API's provider another value": (edit(lambda s: s["api"]["variables"]["AI_PROVIDER"].__setitem__("value", "anthropic")), None,
                                         "the API's AI_PROVIDER must be declared as the worker's"),
    "the API's provider set always": (edit(lambda s: s["api"]["variables"]["AI_PROVIDER"].pop("when")), None,
                                      "the API's AI_PROVIDER must be declared as the worker's"),
    "an AI setting made a secret on both": (edit(lambda s: [s[x]["variables"]["AI_MODEL"].__setitem__("secret", True) for x in ("api", "worker")]),
                                            None, "AI_MODEL is a setting, not a secret"),
    "the photo path's provider on the API": (edit(lambda s: s["api"]["variables"].__setitem__("AI_VISION_PROVIDER", {"value": "disabled"})), None,
                                             "the API reads no other AI setting"),
    "a spend limit on the API": (edit(lambda s: s["api"]["variables"].__setitem__("AI_DAILY_LIMIT_USD", {"value": "10"})), None,
                                 "the API reads no other AI setting"),
    "the Go list is gone": (None, "package config\n", "apiKeyVars (the AI key variables the API refuses) was not found"),
    "the Go list lacks the Meta key": (None, real_read(APIAI, encoding="utf-8").replace('"GEMINUS_MUSE_API_KEY", ', ""),
                                       "lacks a live adapter's key"),
}

failures = 0
if (got := run()) is not None:
    print(f"FAIL the file as committed: {got}")
    failures += 1
else:
    print("PASS the file as committed")
for name, (doc, go, want) in cases.items():
    got = run(doc, go)
    ok = got is not None and want in got
    failures += not ok
    print(f"{'PASS' if ok else 'FAIL'} {name}: {got}")
# A "later" note that differs is no difference.
print("PASS the notes differ" if run(edit(lambda s: s["api"]["variables"]["AI_PROVIDER"].__setitem__("later", "x"))) is None
      else "FAIL the notes differ")
print(f"{len(cases) + 2 - failures}/{len(cases) + 2} cases as expected")
sys.exit(1 if failures else 0)
