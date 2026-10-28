-- DEVELOPMENT-ONLY DOWN MIGRATION of 0018: analyses no longer follow their
-- job, lose the lifecycle guard and the checks of each state's content,
-- and lose their output locale (dropped with its values).
BEGIN;
SET LOCAL ROLE twin_owner;
DROP TRIGGER follow_analysis_job ON twin.jobs;
DROP FUNCTION twin.follow_analysis_job();
DROP TRIGGER analysis_completed_with_its_job ON twin.ai_analyses;
DROP FUNCTION twin.check_analysis_completed_with_its_job();
DROP TRIGGER protect_analysis_lifecycle ON twin.ai_analyses;
DROP FUNCTION twin.protect_analysis_lifecycle();
ALTER TABLE twin.ai_analyses
  DROP CONSTRAINT ai_analyses_provider_metadata_object,
  DROP CONSTRAINT ai_analyses_failed_content,
  DROP CONSTRAINT ai_analyses_analysis_object,
  DROP CONSTRAINT ai_analyses_analysis_iff_completed,
  DROP CONSTRAINT ai_analyses_error_iff_failed,
  DROP CONSTRAINT ai_analyses_baseline_is_another_run,
  DROP CONSTRAINT ai_analyses_question_length,
  DROP COLUMN output_locale;
COMMIT;
