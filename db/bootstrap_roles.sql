-- D-008/D-013 (approved). Run only using a trusted provisioning administrator.
-- NO passwords or application login users are created here.
-- Production credentials, membership grants and TLS are deployment decisions.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='twin_owner') THEN
    CREATE ROLE twin_owner NOLOGIN NOSUPERUSER NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='twin_api') THEN
    CREATE ROLE twin_api NOLOGIN NOSUPERUSER NOBYPASSRLS;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='twin_worker') THEN
    CREATE ROLE twin_worker NOLOGIN NOSUPERUSER NOBYPASSRLS;
  END IF;
  -- D-042: what an operator's provisioning login acts as (cmd/provision-tenant).
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='twin_provisioner') THEN
    CREATE ROLE twin_provisioner NOLOGIN NOSUPERUSER NOBYPASSRLS;
  END IF;
END $$;
-- Abort rather than silently accepting unsafe pre-existing runtime roles.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles
             WHERE rolname IN ('twin_api','twin_worker','twin_provisioner')
               AND (rolsuper OR rolbypassrls OR rolcreaterole OR rolcreatedb OR rolreplication)) THEN
    RAISE EXCEPTION 'unsafe twin runtime role attributes';
  END IF;
END $$;
-- Give the migration login membership in twin_owner through deployment tooling.
-- Give distinct runtime login users only the intended twin_api / twin_worker group.
-- Neither runtime login may inherit twin_owner or any superuser/BYPASSRLS role.
