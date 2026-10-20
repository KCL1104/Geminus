"""Targeted mutations of T-017's code, applied with `go test -overlay` so the
worktree is never modified. Each mutation must make its package's tests
fail (killed); a mutation the tests survive is reported as SURVIVED."""
import json
import os
import subprocess
import sys

W = '/home/user/Geminus/.claude/worktrees/agent-a4dd381c83ba5c83b'
S = '/tmp/claude-0/-home-user-Geminus/b4164204-ed28-580b-a7b5-c02b59026d38/scratchpad/mut'
OUT = W + '/checks/evidence/2026-10-20-T-017/mutations.txt'

M = [
    ('M1 duration tolerance: 1 ms either way', 'internal/import/mapping/mapping.go',
     'return k == 0 || (k == 1 && r > 0)', 'return k == 0 || k == 1 || k == -1', './internal/import/mapping/'),
    ('M2 end before start not checked', 'internal/import/mapping/mapping.go',
     'case e.End.Before(e.Start):', 'case false:', './internal/import/mapping/'),
    ('M3 a field filled twice accepted', 'internal/import/mapping/mapping.go',
     '\t\tif filled[f] {\n\t\t\tout.add(CodeDuplicateTarget', '\t\tif false && filled[f] {\n\t\t\tout.add(CodeDuplicateTarget', './internal/import/mapping/'),
    ('M4 low-confidence entry needs no confirmation', 'internal/import/mapping/mapping.go',
     'if !slices.Contains(confirmed, e.SourceColumn) {', 'if false {', './internal/import/mapping/'),
    ('M5 confirmation of anything accepted', 'internal/import/mapping/mapping.go',
     '\t\tif !needs[c] {', '\t\tif false && !needs[c] {', './internal/import/mapping/'),
    ('M6 leading zeros accepted in integer_ms', 'internal/import/mapping/mapping.go',
     'regexp.MustCompile(`^(0|[1-9][0-9]*)$`)', 'regexp.MustCompile(`^[0-9]+$`)', './internal/import/mapping/'),
    ('M7 transform not checked against the field', 'internal/import/mapping/mapping.go',
     '\t\tif e.Transform != want {', '\t\tif false && e.Transform != want {', './internal/import/mapping/'),
    ('M8 required field may be empty', 'internal/import/mapping/mapping.go',
     '\t\t\tif required(f) {\n\t\t\t\tbad(i, CodeValueMissing', '\t\t\tif false && required(f) {\n\t\t\t\tbad(i, CodeValueMissing', './internal/import/mapping/'),
    ('M9 metadata limit off by one', 'internal/import/mapping/mapping.go',
     'metadata == MaxMetadataColumns+1', 'metadata == MaxMetadataColumns+2', './internal/import/mapping/'),
    ('M10 metadata byte bound one byte late', 'internal/import/mapping/mapping.go',
     '\tif len(doc) > MaxMetadataBytes {', '\tif len(doc) > MaxMetadataBytes+1 {', './internal/import/mapping/'),
    ('M11 issues not bounded', 'internal/import/validation/validation.go',
     '\t\tif len(out.Issues) < lim.MaxIssues {', '\t\tif true {', './internal/import/validation/'),
    ('M12 dataset limit not enforced', 'internal/import/validation/validation.go',
     'if out.DatasetBytes > lim.MaxDatasetBytes {', 'if false && out.DatasetBytes > lim.MaxDatasetBytes {', './internal/import/validation/'),
    ('M13 CSV field-count row passes', 'internal/import/validation/validation.go',
     '\t\tif r.FieldCount != len(r.Header) {', '\t\tif false && r.FieldCount != len(r.Header) {', './internal/import/validation/'),
    ('M14 changed columns not detected', 'internal/import/validation/validation.go',
     '\t\t\tif col >= len(r.Header) || r.Header[col] != c.plan.Name(i) {', '\t\t\tif col >= len(r.Header) {', './internal/import/validation/'),
    ('M15 a job with revision 0 runs', 'internal/import/validator/validator.go',
     '\t\tp.MappingRevision < 1 ||', '\t\tp.MappingRevision < 0 ||', './internal/import/validator/'),
    ('M16 a job for another import runs', 'internal/import/validator/validator.go',
     'p.ImportID != job.Resource.ImportID || ', '', './internal/import/validator/'),
    ('M17 the API may link the validator', 'scripts/check-architecture/rules.go',
     '"./internal/import/profiler/...", "./internal/import/validator/...", "./internal/proposals/generator/..."}, // T-023',
     '"./internal/import/profiler/...", "./internal/proposals/generator/..."}, // T-023', './scripts/check-architecture/'),
]

os.makedirs(S, exist_ok=True)
lines = []
survived = 0
for i, (name, rel, old, new, pkg) in enumerate(M, 1):
    src = open(os.path.join(W, rel), encoding='utf-8').read()
    n = src.count(old)
    if n != 1:
        lines.append(f'{name}: NOT APPLIED ({n} matches of the original text)')
        survived += 1
        continue
    mutated = os.path.join(S, f'm{i}.go')
    open(mutated, 'w', encoding='utf-8').write(src.replace(old, new, 1))
    overlay = os.path.join(S, f'm{i}.json')
    json.dump({'Replace': {os.path.join(W, rel): mutated}}, open(overlay, 'w'))
    r = subprocess.run(['go', 'test', '-count=1', '-overlay=' + overlay, pkg], cwd=W, capture_output=True, text=True, timeout=1800)
    fails = [l for l in r.stdout.splitlines() if l.startswith('--- FAIL') or l.startswith('FAIL') or 'build failed' in l]
    if r.returncode == 0:
        survived += 1
        lines.append(f'{name} [{rel}]: SURVIVED (tests passed)')
    else:
        lines.append(f'{name} [{rel}]: KILLED (exit {r.returncode}): ' + '; '.join(fails[:4]))
lines.append(f'{len(M)} mutations, {len(M) - survived} killed, {survived} survived or not applied')
open(OUT, 'w', encoding='utf-8').write('\n'.join(lines) + '\n')
print('\n'.join(lines))
sys.exit(1 if survived else 0)
