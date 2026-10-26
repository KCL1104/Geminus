-- DEVELOPMENT-ONLY DOWN MIGRATION of 0016: the spend ledger of live AI is
-- dropped with its rows (reservations, organization days, deployment
-- totals). Never on a deployment that has made live calls: the totals are
-- the in-code limits' only memory of what was spent.
BEGIN;
SET LOCAL ROLE twin_owner;
DROP TRIGGER check_ai_spend_day ON twin.ai_spend_reservations;
DROP TRIGGER check_ai_spend_day ON twin.ai_spend_days;
DROP FUNCTION twin.check_ai_spend_day();
DROP TRIGGER protect_ai_spend_reservation ON twin.ai_spend_reservations;
DROP FUNCTION twin.protect_ai_spend_reservation();
DROP TABLE twin.ai_spend_reservations;
DROP TABLE twin.ai_spend_days;
DROP TABLE twin.ai_spend_periods;
COMMIT;
