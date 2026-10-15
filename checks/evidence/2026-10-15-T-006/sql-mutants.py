#!/usr/bin/env python3
"""SQL mutation check for T-006: each mutant (one fix reverted) must make
db/tests/constraints.sql or invariants.sql fail on a fresh ICU en-US database.
Mutated migrations live in the scratchpad; the repository is never modified."""
import pathlib, subprocess, sys

REPO = pathlib.Path("/home/user/Geminus")
HERE = pathlib.Path(__file__).parent
FRESH = HERE / "freshdb.sh"
C1 = "migrations/0001_core.up.sql"
C2 = "migrations/0002_rls.up.sql"

MUTANTS = [
    ("manifest-default-collation", C1, 'ORDER BY logical_ref COLLATE "C"),\'[]\'::jsonb) INTO actual FROM twin.model_version_datasets',
     "ORDER BY logical_ref),'[]'::jsonb) INTO actual FROM twin.model_version_datasets"),
    ("object-key-like", C1, "RETURN starts_with(object_key, prefix) AND", "RETURN object_key LIKE prefix || '%' AND"),
    ("object-key-allows-empty-segment", C1, "AND strpos(object_key, '//') = 0", ""),
    ("object-key-allows-backslash", C1, "AND strpos(object_key, '//') = 0 AND strpos(object_key, E'\\\\') = 0", "AND strpos(object_key, '//') = 0"),
    ("object-key-allows-control", C1, "\n    AND object_key !~ '[[:cntrl:]]';", ";"),
    ("object-key-allows-bare-prefix", C1, "AND length(object_key) > length(prefix)", ""),
    ("object-key-no-length-cap", C1, "\n    AND length(object_key) <= 1024", ""),
    ("risk-not-max", C1, "risk_level = CASE WHEN 'high' IN", "risk_level IS NOT NULL OR risk_level = CASE WHEN 'high' IN"),
    ("risk-without-server", C1, "  CHECK (server_risk_level IS NULL OR risk_level IS NOT NULL)\n", "  CHECK (true)\n"),
    ("no-truncate-guard", C1, "    EXECUTE format('CREATE TRIGGER immutable_table BEFORE TRUNCATE", "    -- EXECUTE format('CREATE TRIGGER immutable_table BEFORE TRUNCATE"),
    ("risk-mutable-after-review", C1, "NEW.risk_level,NEW.server_risk_level,\n         NEW.provider_risk_claim,", "NEW.risk_level,OLD.server_risk_level,\n         OLD.provider_risk_claim,"),
    ("audit-user-any-actor", C1, "  CHECK (actor_type<>'user' OR actor_id ~", "  CHECK (true OR actor_id ~"),
    ("prj-id-case-insensitive", C1, "CREATE DOMAIN twin.prj_id AS text CHECK (VALUE ~ '", "CREATE DOMAIN twin.prj_id AS text CHECK (VALUE ~* '"),
    ("logical-ref-any-case", C1, "VALUE ~ '^dataset:[a-z0-9_.-]+$'", "VALUE ~* '^dataset:[a-z0-9_.-]+$'"),
    ("run-dataset-unit-text", C1, "  unit twin.time_unit NOT NULL,\n  sample_count integer NOT NULL CHECK (sample_count BETWEEN 1 AND 100000),\n  PRIMARY KEY (organization_id,run_id,logical_ref)",
     "  unit text NOT NULL,\n  sample_count integer NOT NULL CHECK (sample_count BETWEEN 1 AND 100000),\n  PRIMARY KEY (organization_id,run_id,logical_ref)"),
    ("run-dataset-sample-cap", C1, "sample_count integer NOT NULL CHECK (sample_count BETWEEN 1 AND 100000),\n  PRIMARY KEY (organization_id,run_id,logical_ref)",
     "sample_count integer NOT NULL CHECK (sample_count>0),\n  PRIMARY KEY (organization_id,run_id,logical_ref)"),
    ("model-id-generic-domain", C1, "  model_id twin.mdl_id NOT NULL,\n  model_version_id twin.mv_id NOT NULL,\n  logical_ref",
     "  model_id twin.public_id NOT NULL,\n  model_version_id twin.mv_id NOT NULL,\n  logical_ref"),
    ("worker-cannot-record-risk", C2, "risk_level,server_risk_level,provider_risk_claim,confidence,", "risk_level,confidence,"),
    # Equivalent: types keep PostgreSQL's default PUBLIC USAGE and DML never checks
    # type USAGE; REVOKE ALL ON SCHEMA twin FROM PUBLIC is the gate. The grant documents intent.
    ("no-domain-usage-grant(equivalent)", C2, "    EXECUTE format('GRANT USAGE ON TYPE twin.%I TO twin_api,twin_worker',d);", "    NULL;"),
]


def run_suite(migrations: dict[str, str]) -> int:
    url = subprocess.run([str(FRESH), "dt_test_mut", "icu"], capture_output=True, text=True, check=True).stdout.strip().splitlines()[-1]
    def psql(path):
        return subprocess.run(["psql", "-X", "-q", url, "-v", "ON_ERROR_STOP=1", "-f", path], capture_output=True, text=True)
    for f in ["db/bootstrap_roles.sql", C1, C2]:
        r = psql(migrations.get(f, str(REPO / f)))
        if r.returncode:
            return 100  # mutant does not even install
    for t in ["db/tests/invariants.sql", "db/tests/constraints.sql"]:
        r = psql(str(REPO / t))
        if r.returncode:
            return r.returncode
    return 0


def main():
    only = set(sys.argv[1:])
    if not only and run_suite({}) != 0:
        print("baseline fails; aborting")
        return 2
    results = []
    for name, rel, old, new in MUTANTS:
        if only and name not in only:
            continue
        src = (REPO / rel).read_text()
        if src.count(old) != 1:
            results.append((name, "BAD-MUTANT"))
            continue
        mpath = HERE / f"mut-{name.replace('(equivalent)', '')}.sql"
        mpath.write_text(src.replace(old, new))
        rc = run_suite({rel: str(mpath)})
        results.append((name, "SURVIVED" if rc == 0 else ("NO-INSTALL" if rc == 100 else "KILLED")))
    for r in results:
        print("\t".join(r))
    bad = [r for r in results if r[1] != "KILLED" and not r[0].endswith("(equivalent)")]
    eq = [r for r in results if r[0].endswith("(equivalent)")]
    killed = sum(1 for r in results if r[1] == "KILLED")
    print(f"{killed}/{len(results) - len(eq)} non-equivalent mutants killed; {len(eq)} documented equivalent")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
