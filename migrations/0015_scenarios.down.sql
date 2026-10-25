-- DEVELOPMENT-ONLY DOWN MIGRATION of 0015: a scenario's creator and
-- creation time are no longer held to the scoped user and the
-- transaction's time, its name to 1–200 characters, and the list's index
-- is dropped. Scenarios stay immutable (0001).
BEGIN;
SET LOCAL ROLE twin_owner;
DROP TRIGGER protect_scenario_creation ON twin.scenarios;
DROP FUNCTION twin.protect_scenario_creation();
DROP INDEX twin.scenarios_base_idx;
ALTER TABLE twin.scenarios DROP CONSTRAINT scenarios_name_length;
COMMIT;
