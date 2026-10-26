-- The deployment's four database logins (T-039; docs/runbooks/deploy.md §3).
-- Run once per database, after db/bootstrap_roles.sql, as an administrator
-- that may create roles and grant the twin roles (on Railway: the managed
-- database's postgres login), with psql 15 or later:
--
--   GEMINUS_MIGRATOR_DB_PASSWORD=… GEMINUS_API_DB_PASSWORD=… \
--   GEMINUS_WORKER_DB_PASSWORD=… GEMINUS_PROVISIONER_DB_PASSWORD=… \
--     psql "$ADMIN_DATABASE_URL" -v ON_ERROR_STOP=1 -f deploy/sql/create_logins.sql
--
-- The passwords come from the environment (\getenv), never from this file or
-- the command line: at least 32 characters each, all four different (e.g.
-- `openssl rand -hex 32`, so they need no escaping in a URL). The server
-- stores them hashed (password_encryption, scram-sha-256 by default); a
-- failing CREATE ROLE statement would be written to the server log with its
-- text, which is why every statement is guarded and nothing here can fail
-- after the checks.
--
--   geminus_migrator     runs migrations/ (cmd/migrate): in twin_owner, nothing else
--   geminus_api          the API's runtime login: in twin_api only (tenant.CheckRuntimeRole)
--   geminus_worker       the worker's runtime login: in twin_worker only
--   geminus_provisioner  cmd/provision-tenant (D-042): NOINHERIT, may SET ROLE twin_provisioner only
--
-- No login is a superuser or has BYPASSRLS, CREATEROLE, CREATEDB or
-- REPLICATION, and none is granted anything directly. twin_owner gets CREATE
-- on this database (the migrations create the twin schema, and
-- cmd/migrate its geminus_migrations record). A login that already exists
-- is left as it is (password and attributes); rotate a password with
-- ALTER ROLE … PASSWORD from the same kind of environment variable.
\set ON_ERROR_STOP on
\getenv migrator_password GEMINUS_MIGRATOR_DB_PASSWORD
\getenv api_password GEMINUS_API_DB_PASSWORD
\getenv worker_password GEMINUS_WORKER_DB_PASSWORD
\getenv provisioner_password GEMINUS_PROVISIONER_DB_PASSWORD
\if :{?migrator_password}
\else
DO $$ BEGIN RAISE EXCEPTION 'GEMINUS_MIGRATOR_DB_PASSWORD is not set'; END $$;
\endif
\if :{?api_password}
\else
DO $$ BEGIN RAISE EXCEPTION 'GEMINUS_API_DB_PASSWORD is not set'; END $$;
\endif
\if :{?worker_password}
\else
DO $$ BEGIN RAISE EXCEPTION 'GEMINUS_WORKER_DB_PASSWORD is not set'; END $$;
\endif
\if :{?provisioner_password}
\else
DO $$ BEGIN RAISE EXCEPTION 'GEMINUS_PROVISIONER_DB_PASSWORD is not set'; END $$;
\endif
SELECT length(:'migrator_password') >= 32 AND length(:'api_password') >= 32
   AND length(:'worker_password') >= 32 AND length(:'provisioner_password') >= 32 AS long_enough,
       cardinality(ARRAY(SELECT DISTINCT p FROM unnest(ARRAY[:'migrator_password', :'api_password',
         :'worker_password', :'provisioner_password']) AS p)) = 4 AS all_different,
       EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'twin_owner')
   AND EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'twin_api')
   AND EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'twin_worker')
   AND EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'twin_provisioner') AS bootstrapped \gset
\if :long_enough
\else
DO $$ BEGIN RAISE EXCEPTION 'each GEMINUS_*_DB_PASSWORD must have at least 32 characters'; END $$;
\endif
\if :all_different
\else
DO $$ BEGIN RAISE EXCEPTION 'the four GEMINUS_*_DB_PASSWORD values must be different'; END $$;
\endif
\if :bootstrapped
\else
DO $$ BEGIN RAISE EXCEPTION 'run db/bootstrap_roles.sql first: the twin roles are missing'; END $$;
\endif
SELECT format('CREATE ROLE geminus_migrator LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB NOREPLICATION INHERIT IN ROLE twin_owner PASSWORD %L',
              :'migrator_password')
 WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'geminus_migrator') \gexec
SELECT format('CREATE ROLE geminus_api LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB NOREPLICATION INHERIT IN ROLE twin_api PASSWORD %L',
              :'api_password')
 WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'geminus_api') \gexec
SELECT format('CREATE ROLE geminus_worker LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB NOREPLICATION INHERIT IN ROLE twin_worker PASSWORD %L',
              :'worker_password')
 WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'geminus_worker') \gexec
SELECT format('CREATE ROLE geminus_provisioner LOGIN NOSUPERUSER NOBYPASSRLS NOCREATEROLE NOCREATEDB NOREPLICATION NOINHERIT IN ROLE twin_provisioner PASSWORD %L',
              :'provisioner_password')
 WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'geminus_provisioner') \gexec
GRANT CREATE ON DATABASE :"DBNAME" TO twin_owner;
\unset migrator_password
\unset api_password
\unset worker_password
\unset provisioner_password
SELECT rolname, rolsuper, rolbypassrls, rolcreaterole, rolcreatedb, rolinherit,
       ARRAY(SELECT g.rolname FROM pg_auth_members m JOIN pg_roles g ON g.oid = m.roleid WHERE m.member = r.oid ORDER BY 1) AS member_of
  FROM pg_roles r WHERE rolname LIKE 'geminus\_%' ORDER BY rolname;
