#!/usr/bin/env python3
"""Check decision-register integrity and derive which human decisions gate which tasks.

Never approves anything. An approval is only well-formed when a human filled
approved_by/approved_at; this script only checks that shape and reports gates.
"""
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DECISION_STATES = {'proposed', 'approved', 'rejected', 'superseded'}
TASK_STATES = {'not_started', 'in_progress', 'blocked', 'review', 'done'}
DATE = re.compile(r'^\d{4}-\d{2}-\d{2}')


def main():
    register = json.loads((ROOT/'planning/decisions.json').read_text(encoding='utf-8'))
    decisions = register['decisions']
    by_id = {d['id']: d for d in decisions}
    assert len(by_id) == len(decisions), 'duplicate decision IDs'
    for d in decisions:
        assert re.fullmatch(r'D-\d{3}', d['id']), d['id']
        assert d['status'] in DECISION_STATES, (d['id'], d['status'])
        if d['status'] == 'proposed':
            assert d['approved_by'] is None and d['approved_at'] is None, \
                f"{d['id']}: proposed decisions must not carry an approver"
        else:
            assert isinstance(d['approved_by'], str) and d['approved_by'].strip(), d['id']
            assert isinstance(d['approved_at'], str) and DATE.match(d['approved_at']), d['id']
    human = register.get('human_decisions', [])
    assert len({h['id'] for h in human}) == len(human), 'duplicate human decision IDs'
    for h in human:
        assert re.fullmatch(r'H-\d{3}', h['id']), h['id']
        for key in ('subject', 'decision', 'decided_by', 'source'):
            assert isinstance(h[key], str) and h[key].strip(), (h['id'], key)
        assert DATE.match(h['decided_at']), h['id']

    tasks = json.loads((ROOT/'planning/tasks.json').read_text(encoding='utf-8'))['tasks']
    index = {t['id']: t for t in tasks}
    for t in tasks:
        status = t['status']
        assert status in TASK_STATES, (t['id'], status)
        deps = [index[x]['status'] for x in t['depends_on']]
        if status in ('in_progress', 'review'):
            assert all(s in ('review', 'done') for s in deps), f"{t['id']}: started before its dependencies"
        if status in ('review', 'done'):
            assert t['completion_evidence'], f"{t['id']}: {status} without completion_evidence"
        if status == 'done':
            assert all(s == 'done' for s in deps), f"{t['id']}: done before its dependencies"
            assert all(by_id[g]['status'] == 'approved' for g in t['decision_gates']), \
                f"{t['id']}: done with an unapproved decision gate"
        if status == 'blocked':
            assert t.get('blocked_reason'), f"{t['id']}: blocked without blocked_reason"

    gates = {d['id']: {'status': d['status'], 'gate': d['gate'],
                       'gates_tasks': [t['id'] for t in tasks if d['id'] in t['decision_gates']]}
             for d in decisions}
    open_tasks = [t for t in tasks if t['status'] == 'not_started']
    ready = [t['id'] for t in open_tasks
             if all(index[x]['status'] == 'done' for x in t['depends_on'])
             and all(by_id[g]['status'] == 'approved' for g in t['decision_gates'])]
    after_review = [{'id': t['id'],
                     'awaiting_task_review': [x for x in t['depends_on'] if index[x]['status'] == 'review'],
                     'awaiting_decisions': [g for g in t['decision_gates'] if by_id[g]['status'] != 'approved']}
                    for t in open_tasks
                    if all(index[x]['status'] in ('review', 'done') for x in t['depends_on'])
                    and t['id'] not in ready]
    report = {'status': 'PASS_REGISTER_SHAPE_ONLY',
              'decisions_by_status': {s: sum(d['status'] == s for d in decisions) for s in sorted(DECISION_STATES)},
              'human_decisions_recorded': [h['id'] for h in human],
              'tasks_by_status': {s: sum(t['status'] == s for t in tasks) for s in sorted(TASK_STATES)},
              'ready_tasks': ready, 'next_after_review': after_review, 'gates': gates,
              'note': 'Shape checks only; whether a recorded approval is genuine is for human review.'}
    (ROOT/'checks/decisions-check.json').write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n', encoding='utf-8')
    print(json.dumps({k: v for k, v in report.items() if k != 'gates'}, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()
