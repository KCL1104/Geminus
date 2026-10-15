#!/usr/bin/env python3
"""Static SQL inventory ONLY. Does not parse or execute PostgreSQL SQL."""
import json,re
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]

def main():
    core=(ROOT/'migrations/0001_core.up.sql').read_text()
    rls=(ROOT/'migrations/0002_rls.up.sql').read_text()
    tables=re.findall(r'CREATE TABLE twin\.(\w+) \((.*?)\n\);',core,re.S)
    assert len(tables)==20,f'Expected 20 draft tables, found {len(tables)}'
    for name,body in tables:
        if name=='users':continue
        assert re.search(r'organization_id\s+twin\.org_id\s+NOT NULL',body),name
        assert f"'{name}'" in rls,name
    assert 'FORCE ROW LEVEL SECURITY' in rls
    assert 'WITH CHECK' in rls
    assert 'immutable_record' in core
    assert 'lease_token' in core
    assert 'Worker can never INSERT/PUBLISH' in rls
    # CB-14: '_' in organization IDs is a LIKE wildcard; key checks use starts_with.
    assert not re.search(r'\bLIKE\b', re.sub(r'--[^\n]*', '', core)), 'LIKE in migrations/0001_core.up.sql'
    assert 'COLLATE "C"' in core
    report={'status':'PASS_STATIC_INVENTORY_ONLY','tables':len(tables),'tenant_owned_tables':len(tables)-1,
            'postgresql_parse':'NOT_RUN_NO_PARSER','postgresql_migration_execution':'NOT_RUN_NO_SERVER_CLIENT',
            'rls_runtime_tests':'NOT_RUN','note':'Keyword/inventory checks do not prove SQL syntax, policies, privileges or execution correctness; execution evidence comes from make db-test / make db-go-test on a disposable database (CI db job).'}
    (ROOT/'checks/sql-inventory.json').write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps(report,indent=2))
if __name__=='__main__':main()
