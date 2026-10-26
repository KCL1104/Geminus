#!/usr/bin/env python3
"""The T-056 follow-up (D-124): the negative cases of scripts/check_deploy.py's
rules for the API's AI settings, now seven (the proposals' four and the photo
path's AI_VISION_PROVIDER, AI_VISION_POLICY_APPROVAL, AI_VISION_MODEL). Each
case changes deploy/railway/variables.json (or the Go list it reads) in memory
only, by replacing what pathlib.Path.read_text returns for that one file, and
expects railway() to fail with the rule's message. Nothing on disk changes;
no network. Run from the repository root:
    python -I checks/evidence/2026-10-26-T-056-apivision/check_deploy_negatives.py
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
        out = mod.railway(mod.code_variables())
        return None, out
    except AssertionError as e:
        return str(e), None
    finally:
        pathlib.Path.read_text = real_read


def edit(fn):
    d = copy.deepcopy(base)
    fn(d["services"])
    return d


def both(name, key, value):
    def fn(s):
        for svc in ("api", "worker"):
            s[svc]["variables"][name][key] = value
    return fn


setting = {"secret": False, "source": "owner", "required": False}
cases = {
    # The photo path's settings (new).
    "the API has no AI_VISION_PROVIDER": (edit(lambda s: s["api"]["variables"].pop("AI_VISION_PROVIDER")), None,
                                          "AI_VISION_PROVIDER must be declared on the API and on the worker"),
    "the worker has no AI_VISION_MODEL": (edit(lambda s: s["worker"]["variables"].pop("AI_VISION_MODEL")), None,
                                          "AI_VISION_MODEL must be declared on the API and on the worker"),
    "neither has AI_VISION_POLICY_APPROVAL": (edit(lambda s: [s[x]["variables"].pop("AI_VISION_POLICY_APPROVAL") for x in ("api", "worker")]),
                                              None, "AI_VISION_POLICY_APPROVAL must be declared on the API and on the worker"),
    "the API's photo provider set by the deployer": (edit(lambda s: s["api"]["variables"].__setitem__("AI_VISION_PROVIDER", {"value": "meta"})),
                                                     None, "the API's AI_VISION_PROVIDER must be declared as the worker's"),
    "the API's photo model required": (edit(lambda s: s["api"]["variables"]["AI_VISION_MODEL"].__setitem__("required", True)), None,
                                       "the API's AI_VISION_MODEL must be declared as the worker's"),
    "the API's photo approval generated": (edit(lambda s: s["api"]["variables"].__setitem__("AI_VISION_POLICY_APPROVAL",
                                                                                              {"secret": False, "source": "owner"})),
                                           None, "the API's AI_VISION_POLICY_APPROVAL must be declared as the worker's"),
    "the photo provider a secret on both": (edit(both("AI_VISION_PROVIDER", "secret", True)), None,
                                            "AI_VISION_PROVIDER is a setting, not a secret"),
    "the test provider for photos on both": (edit(lambda s: [s[x]["variables"].__setitem__("AI_VISION_PROVIDER", {"value": "fake"})
                                                             for x in ("api", "worker")]),
                                             None, "mentions 'fake'"),
    # The worker's other AI settings stay off the API.
    "a spend limit on the API": (edit(lambda s: s["api"]["variables"].__setitem__("AI_DAILY_LIMIT_USD", {"value": "10"})), None,
                                 "the API reads no other AI setting"),
    "a budget setting on the API": (edit(lambda s: s["api"]["variables"].__setitem__("AI_MAX_RETRIES", {"value": "1"})), None,
                                    "the API reads no other AI setting"),
    # The proposals' settings and the keys, as before (regression).
    "the API has no AI_MODEL": (edit(lambda s: s["api"]["variables"].pop("AI_MODEL")), None,
                                "AI_MODEL must be declared on the API and on the worker"),
    "the API's switch set by the deployer": (edit(lambda s: s["api"]["variables"].__setitem__("GEMINUS_AI_LIVE", {"value": "on"})), None,
                                             "the API's GEMINUS_AI_LIVE must be declared as the worker's"),
    "the API holds the Meta key": (edit(lambda s: (s["api"]["forbidden"].remove("GEMINUS_MUSE_API_KEY"),
                                                   s["api"]["variables"].__setitem__("GEMINUS_MUSE_API_KEY", {"secret": True, "source": "owner", "required": False}))),
                                   None, "must forbid every AI key variable it refuses"),
    "the API does not forbid ANTHROPIC_AUTH_TOKEN": (edit(lambda s: s["api"]["forbidden"].remove("ANTHROPIC_AUTH_TOKEN")), None,
                                                     "must forbid every AI key variable it refuses"),
    "the Go list lacks the Meta key": (None, real_read(APIAI, encoding="utf-8").replace('"GEMINUS_MUSE_API_KEY", ', ""),
                                       "lacks a live adapter's key"),
}

failures = 0
got, out = run()
if got is not None or out["api_ai"]["settings"] != list(mod.API_AI_SETTINGS) or len(out["api_ai"]["settings"]) != 7:
    print(f"FAIL the file as committed: {got} {out and out['api_ai']}")
    failures += 1
else:
    print(f"PASS the file as committed: api_ai {out['api_ai']['settings']}")
for name, (doc, go, want) in cases.items():
    got, _ = run(doc, go)
    ok = got is not None and want in got
    failures += not ok
    print(f"{'PASS' if ok else 'FAIL'} {name}: {got}")
# A "later" note that differs is no difference; the same setting declared
# alike but for the note passes.
for name, fn in {"the notes differ": lambda s: s["api"]["variables"]["AI_PROVIDER"].__setitem__("later", "x"),
                 "a note on the API's photo provider": lambda s: s["api"]["variables"]["AI_VISION_PROVIDER"].__setitem__("later", "x")}.items():
    got, _ = run(edit(fn))
    failures += got is not None
    print(f"{'PASS' if got is None else 'FAIL'} {name}: {got}")
total = len(cases) + 3
print(f"{total - failures}/{total} cases as expected")
sys.exit(1 if failures else 0)
