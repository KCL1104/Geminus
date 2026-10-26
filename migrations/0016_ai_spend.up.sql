-- T-053 (H-018; PROPOSED D-110, D-111; REQ-016, REQ-019; AGENTS.md §2):
-- the spend ledger of live AI. Before a provider call the worker reserves
-- the most it can cost; after it, settles what it cost. Counting is in the
-- database so that every worker process (and every attempt, retry and
-- failover) shares one count, and a conditional UPDATE on a row lock makes
-- concurrent reservations serialize: none can take a limit past its value.
--
--   * twin.ai_spend_periods holds the deployment's totals, both provider
--     organizations combined (H-018's monthly budget): one row per UTC day
--     and one per UTC calendar month. Totals only, no organization and no
--     call; it is not tenant data, so it has no organization_id, and its
--     policy lets any transaction that is scoped to an organization (every
--     worker transaction) use it. Only the worker has privileges on it.
--   * twin.ai_spend_days holds one organization's total of a UTC day
--     (tenant data: organization_id, RLS as 0002).
--   * twin.ai_spend_reservations holds every reservation: its charge, what
--     it is for (a proposal, an analysis or an import of the same
--     organization: composite foreign keys, as twin.jobs), and once settled
--     what the call cost and its billed tokens. Prompt, answer and key are
--     never held. A reservation is settled once; then it never changes;
--     nobody deletes one (no DELETE grant).
--   * A day's committed amount is what its reservations hold: the reserved
--     amount of the open ones, the settled amount of the settled ones. A
--     deferred constraint trigger checks it at commit for every
--     organization day a transaction touched, so a write that changes one
--     without the other cannot commit. The deployment's totals sum every
--     organization's reservations, which no scoped transaction can see
--     (RLS), so they are kept by the same writes and checked by the Go
--     tests, not by SQL.
--   * Amounts are bigint nano-USD (internal/ai/spend). The day of a
--     reservation is the worker's UTC day (the meter's clock), not checked
--     against now(): the tests use days of their own.
BEGIN;
SET LOCAL ROLE twin_owner;

-- D-001: a reservation's ID is "asr_" + ULID, with a domain of its own as
-- 0001's kinds have; USAGE as 0002 and 0004 grant every domain.
CREATE DOMAIN twin.asr_id AS text CHECK (VALUE ~ '^asr_[0-7][0-9A-HJKMNP-TV-Z]{25}$');
GRANT USAGE ON TYPE twin.asr_id TO twin_api,twin_worker,twin_provisioner;

CREATE TABLE twin.ai_spend_periods (
  period text NOT NULL CHECK (period IN ('day','month')),
  starts_on date NOT NULL,
  committed_nano_usd bigint NOT NULL DEFAULT 0 CHECK (committed_nano_usd >= 0),
  PRIMARY KEY (period, starts_on),
  CONSTRAINT ai_spend_periods_month_starts CHECK (period = 'day' OR extract(day FROM starts_on) = 1)
);

CREATE TABLE twin.ai_spend_days (
  organization_id twin.org_id NOT NULL REFERENCES twin.organizations(id),
  day date NOT NULL,
  committed_nano_usd bigint NOT NULL DEFAULT 0 CHECK (committed_nano_usd >= 0),
  PRIMARY KEY (organization_id, day)
);

CREATE TABLE twin.ai_spend_reservations (
  id twin.asr_id NOT NULL,
  organization_id twin.org_id NOT NULL REFERENCES twin.organizations(id),
  day date NOT NULL,
  role text NOT NULL CHECK (role IN ('modeler','scenario','analyst','importer')),
  model text NOT NULL CHECK (model ~ '^[a-z0-9][a-z0-9.-]{0,127}$'),
  ai_proposal_id twin.aip_id,
  ai_analysis_id twin.ana_id,
  import_id twin.imp_id,
  request_id twin.req_id NOT NULL,
  reserved_nano_usd bigint NOT NULL CHECK (reserved_nano_usd > 0),
  state text NOT NULL DEFAULT 'reserved' CHECK (state IN ('reserved','settled')),
  outcome text CHECK (outcome IN ('usage','none','unknown')),
  settled_nano_usd bigint CHECK (settled_nano_usd >= 0),
  input_tokens bigint CHECK (input_tokens >= 0),
  output_tokens bigint CHECK (output_tokens >= 0),
  cache_write_tokens bigint CHECK (cache_write_tokens >= 0),
  cache_read_tokens bigint CHECK (cache_read_tokens >= 0),
  unknown_attempts integer CHECK (unknown_attempts >= 0),
  credential_label text CHECK (credential_label ~ '^[a-z][a-z0-9-]{0,31}$'),
  created_at timestamptz NOT NULL DEFAULT now(),
  settled_at timestamptz,
  PRIMARY KEY (organization_id, id),
  UNIQUE (id),
  FOREIGN KEY (organization_id, day) REFERENCES twin.ai_spend_days(organization_id, day),
  FOREIGN KEY (organization_id, ai_proposal_id) REFERENCES twin.ai_proposals(organization_id, id),
  FOREIGN KEY (organization_id, ai_analysis_id) REFERENCES twin.ai_analyses(organization_id, id),
  FOREIGN KEY (organization_id, import_id) REFERENCES twin.imports(organization_id, id),
  CONSTRAINT ai_spend_reservations_one_target CHECK (num_nonnulls(ai_proposal_id, ai_analysis_id, import_id) = 1),
  CONSTRAINT ai_spend_reservations_open CHECK (state <> 'reserved' OR (outcome IS NULL AND settled_nano_usd IS NULL
    AND input_tokens IS NULL AND output_tokens IS NULL AND cache_write_tokens IS NULL AND cache_read_tokens IS NULL
    AND unknown_attempts IS NULL AND credential_label IS NULL AND settled_at IS NULL)),
  CONSTRAINT ai_spend_reservations_settled CHECK (state <> 'settled' OR (outcome IS NOT NULL AND settled_nano_usd IS NOT NULL
    AND input_tokens IS NOT NULL AND output_tokens IS NOT NULL AND cache_write_tokens IS NOT NULL AND cache_read_tokens IS NOT NULL
    AND unknown_attempts IS NOT NULL AND settled_at IS NOT NULL)),
  -- nothing billed is nothing spent; an unknown outcome had an attempt
  -- without an answer
  CONSTRAINT ai_spend_reservations_none CHECK (outcome IS DISTINCT FROM 'none' OR settled_nano_usd = 0),
  CONSTRAINT ai_spend_reservations_unknown CHECK (outcome IS DISTINCT FROM 'unknown' OR unknown_attempts > 0)
);
CREATE INDEX ai_spend_reservations_day_idx ON twin.ai_spend_reservations(organization_id, day);

-- RLS: the organization's rows for tenant data (as 0002); the deployment's
-- totals for any scoped transaction (the worker's are always scoped).
ALTER TABLE twin.ai_spend_days ENABLE ROW LEVEL SECURITY;
ALTER TABLE twin.ai_spend_days FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON twin.ai_spend_days FOR ALL
  USING (organization_id::text = twin.current_organization_id()) WITH CHECK (organization_id::text = twin.current_organization_id());
ALTER TABLE twin.ai_spend_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE twin.ai_spend_reservations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON twin.ai_spend_reservations FOR ALL
  USING (organization_id::text = twin.current_organization_id()) WITH CHECK (organization_id::text = twin.current_organization_id());
ALTER TABLE twin.ai_spend_periods ENABLE ROW LEVEL SECURITY;
ALTER TABLE twin.ai_spend_periods FORCE ROW LEVEL SECURITY;
CREATE POLICY scoped_deployment_totals ON twin.ai_spend_periods FOR ALL
  USING (twin.current_organization_id() IS NOT NULL) WITH CHECK (twin.current_organization_id() IS NOT NULL);

-- Grants: the worker only (the API calls no provider: live backends link
-- only into the worker, architecture rule live-ai-only-in-the-worker). The
-- worker changes only the amounts and a reservation's settlement; it
-- deletes nothing.
REVOKE ALL ON twin.ai_spend_periods, twin.ai_spend_days, twin.ai_spend_reservations FROM PUBLIC;
GRANT SELECT, INSERT ON twin.ai_spend_periods, twin.ai_spend_days, twin.ai_spend_reservations TO twin_worker;
GRANT UPDATE (committed_nano_usd) ON twin.ai_spend_periods, twin.ai_spend_days TO twin_worker;
GRANT UPDATE (state, outcome, settled_nano_usd, input_tokens, output_tokens, cache_write_tokens, cache_read_tokens,
  unknown_attempts, credential_label, settled_at) ON twin.ai_spend_reservations TO twin_worker;

-- A reservation opens at its transaction's time, is settled once, at its
-- transaction's time, and then never changes; nothing else of it changes.
-- Members of twin_owner (operators, the SQL tests) are exempt, as in 0015.
CREATE FUNCTION twin.protect_ai_spend_reservation() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF pg_catalog.pg_has_role(current_user, 'twin_owner', 'USAGE') THEN
    RETURN NEW;
  END IF;
  IF TG_OP = 'INSERT' THEN
    IF NEW.state <> 'reserved' THEN
      RAISE EXCEPTION 'a reservation opens unsettled' USING ERRCODE = '55000';
    END IF;
    IF NEW.created_at IS DISTINCT FROM now() THEN
      RAISE EXCEPTION 'a reservation opens at the time of its transaction' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
  END IF;
  IF OLD.state <> 'reserved' THEN
    RAISE EXCEPTION 'a settled reservation never changes' USING ERRCODE = '55000';
  END IF;
  IF NEW.state <> 'settled' THEN
    RAISE EXCEPTION 'a reservation changes only to be settled' USING ERRCODE = '55000';
  END IF;
  IF NEW.settled_at IS DISTINCT FROM now() THEN
    RAISE EXCEPTION 'a reservation is settled at the time of its transaction' USING ERRCODE = '55000';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION twin.protect_ai_spend_reservation() FROM PUBLIC; -- runs as a trigger, never called directly
CREATE TRIGGER protect_ai_spend_reservation BEFORE INSERT OR UPDATE ON twin.ai_spend_reservations
  FOR EACH ROW EXECUTE FUNCTION twin.protect_ai_spend_reservation();

-- At commit, every organization day the transaction touched holds exactly
-- what its reservations hold (for every role: the owner's SQL keeps the
-- ledger consistent too). It runs as the caller, under RLS, in the
-- caller's organization: the day's own rows only.
CREATE FUNCTION twin.check_ai_spend_day() RETURNS trigger LANGUAGE plpgsql
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  org text := NEW.organization_id; -- both tables name the day by these two columns
  d date := NEW.day;
  held bigint;
  held_by_reservations bigint;
BEGIN
  SELECT committed_nano_usd INTO held FROM twin.ai_spend_days WHERE organization_id = org AND day = d;
  SELECT coalesce(sum(CASE WHEN state = 'reserved' THEN reserved_nano_usd ELSE settled_nano_usd END), 0)
    INTO held_by_reservations FROM twin.ai_spend_reservations WHERE organization_id = org AND day = d;
  IF held IS DISTINCT FROM held_by_reservations THEN
    RAISE EXCEPTION 'an organization day holds what its reservations hold' USING ERRCODE = '23514';
  END IF;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION twin.check_ai_spend_day() FROM PUBLIC;
CREATE CONSTRAINT TRIGGER check_ai_spend_day AFTER INSERT OR UPDATE ON twin.ai_spend_days
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION twin.check_ai_spend_day();
CREATE CONSTRAINT TRIGGER check_ai_spend_day AFTER INSERT OR UPDATE ON twin.ai_spend_reservations
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION twin.check_ai_spend_day();

COMMIT;
