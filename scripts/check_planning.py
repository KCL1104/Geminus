#!/usr/bin/env python3
"""Check task DAG, source refs and requirement/decision coverage. Does not mark work done."""
from pathlib import Path
import csv
import json
ROOT=Path(__file__).resolve().parents[1]

def main():
    data=json.loads((ROOT/'planning/tasks.json').read_text())
    tasks=data['tasks']; index={t['id']:t for t in tasks}
    assert len(tasks)==len(index),'duplicate task IDs'
    decisions=json.loads((ROOT/'planning/decisions.json').read_text())['decisions']
    dids={d['id'] for d in decisions}
    reqs={f'REQ-{i:03d}' for i in range(1,26)}
    tests={f'TEST-{i:03d}' for i in range(1,37)}
    for t in tasks:
        assert set(t['depends_on']) <= set(index)
        assert t['id'] not in t['depends_on']
        assert set(t['decision_gates']) <= dids
        assert set(t['requirement_ids']) <= reqs
        assert set(t['acceptance_tests']) <= tests
        for p in t['spec_refs']: assert (ROOT/p).is_file(),(t['id'],p)
        assert t['scope'] and t['definition_of_done'] and t['verification_commands']
    visited=set();active=set();order=[]
    def visit(tid):
        if tid in active: raise ValueError(f'dependency cycle at {tid}')
        if tid in visited:return
        active.add(tid)
        for dep in index[tid]['depends_on']:visit(dep)
        active.remove(tid);visited.add(tid);order.append(tid)
    for tid in index:visit(tid)
    with (ROOT/'planning/traceability.csv').open(encoding='utf-8-sig',newline='') as f:
        trace=list(csv.DictReader(f))
    assert {r['requirement_id'] for r in trace}==reqs
    for r in trace:
        assert set(r['task_ids'].split(';'))<=set(index)
        assert set(r['test_ids'].split(';'))<=tests
    report={'status':'PASS','tasks':len(tasks),'dependency_graph':'ACYCLIC',
            'topological_order':order,'requirements_with_traceability':len(trace),
            'proposed_decisions':sum(d['status']=='proposed' for d in decisions),
            'completed_tasks':sum(t['status']=='done' for t in tasks)}
    (ROOT/'checks/planning-check.json').write_text(json.dumps(report,indent=2)+'\n')
    print(json.dumps(report,indent=2))
if __name__=='__main__':main()
