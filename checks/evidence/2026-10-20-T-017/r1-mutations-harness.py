"""Targeted mutations of T-017's code after review round 1, applied with
`go test -overlay` so the worktree is never modified. Each mutation must
make its package's tests fail (killed). Round 0's set (M1-M17, re-anchored
where round 1 changed the code), the reviewer's survivors that L4 asked to
cover (R17, R19, R21, R24, R26, R27) and round 1's own (N1-N10)."""
import json
import os
import subprocess
import sys

W = '/home/user/Geminus/.claude/worktrees/agent-a4dd381c83ba5c83b'
S = '/tmp/claude-0/-home-user-Geminus/b4164204-ed28-580b-a7b5-c02b59026d38/scratchpad/mut2'
OUT = W + '/checks/evidence/2026-10-20-T-017/r1-mutations.txt'
MAP, VAL, VTR, UPL, PRO = ('internal/import/mapping/mapping.go', 'internal/import/validation/validation.go',
                           'internal/import/validator/validator.go', 'internal/import/upload/mapping.go', 'internal/import/profile/infer.go')
PM, PV, PR, PU, PP = './internal/import/mapping/', './internal/import/validation/', './internal/import/validator/', './internal/import/upload/', './internal/import/profile/'

M = [
    ('M1 duration tolerance: 1 ms either way', MAP, 'return k == 0 || (k == 1 && r > 0)', 'return k == 0 || k == 1 || k == -1', PM),
    ('M2 end before start not checked', MAP, 'case e.End.Before(e.Start):', 'case false:', PM),
    ('M3 a field filled twice accepted', MAP, '\t\tif filled[f] {\n\t\t\tout.add(CodeDuplicateTarget', '\t\tif false && filled[f] {\n\t\t\tout.add(CodeDuplicateTarget', PM),
    ('M4 a column taken as suggested needs no confirmation', MAP, 'if !slices.Contains(confirmed, column) {', 'if false {', PM),
    ('M5 confirmation of anything accepted', MAP, '\t\tif !needs[c] {', '\t\tif false && !needs[c] {', PM),
    ('M6 leading zeros accepted in integer_ms', MAP, 'regexp.MustCompile(`^(0|[1-9][0-9]*)$`)', 'regexp.MustCompile(`^[0-9]+$`)', PM),
    ('M7 transform not checked against the field', MAP, '\t\tif e.Transform != want {', '\t\tif false && e.Transform != want {', PM),
    ('M8 required field may be empty', MAP, '\t\t\tif required(f) {\n\t\t\t\tbad(i, CodeValueMissing', '\t\t\tif false && required(f) {\n\t\t\t\tbad(i, CodeValueMissing', PM),
    ('M9 metadata limit off by one', MAP, 'metadata == MaxMetadataColumns+1', 'metadata == MaxMetadataColumns+2', PM),
    ('M10 metadata byte bound one byte late', MAP, '\tif len(doc) > MaxMetadataBytes {', '\tif len(doc) > MaxMetadataBytes+1 {', PM),
    ('M11 issues not bounded', VAL, '\t\tif len(out.Issues) < lim.MaxIssues {', '\t\tif true {', PV),
    ('M12 dataset limit not enforced', VAL, 'if out.DatasetBytes > lim.MaxDatasetBytes {', 'if false && out.DatasetBytes > lim.MaxDatasetBytes {', PV),
    ('M13 CSV field-count row passes', VAL, '\t\tif r.FieldCount != len(r.Header) {', '\t\tif false && r.FieldCount != len(r.Header) {', PV),
    ('M14 changed columns not detected', VAL, '\t\t\tif col >= len(r.Header) || r.Header[col] != c.plan.Name(i) {', '\t\t\tif col >= len(r.Header) {', PV),
    ('M15 a job with revision 0 runs', VTR, '\t\tp.MappingRevision < 1 ||', '\t\tp.MappingRevision < 0 ||', PR),
    ('M16 a job for another import runs', VTR, 'p.ImportID != job.Resource.ImportID || ', '', PR),
    ('M17 the API may link the validator', 'scripts/check-architecture/rules.go',
     '"./internal/import/profiler/...", "./internal/import/validator/...", "./internal/proposals/generator/..."}, // T-023',
     '"./internal/import/profiler/...", "./internal/proposals/generator/..."}, // T-023', './scripts/check-architecture/'),
    # The reviewer's survivors (L4).
    ('R17 JSON Pointer escapes swapped', VAL, "b = append(b, '~', '0')", "b = append(b, '~', '1')", PV),
    ('R19 preview byte bound one byte late', VAL, 'if p.size+cost > p.lim.PreviewBytes {', 'if p.size+cost > p.lim.PreviewBytes+1 {', PV),
    ('R21 Write ignores a row that no longer maps', VAL,
     '\t\te, issues := plan.Apply(values)\n\t\tif len(issues) > 0 {\n\t\t\treturn ErrChanged',
     '\t\te, issues := plan.Apply(values)\n\t\tif false && len(issues) > 0 {\n\t\t\treturn ErrChanged', PV),
    ('R24 upload hash/size not checked (validator)', VTR,
     'if c.n != imp.Size || "sha256:"+hex.EncodeToString(c.h.Sum(nil)) != imp.UploadSHA256 {',
     'if false && c.n != imp.Size || false && "sha256:"+hex.EncodeToString(c.h.Sum(nil)) != imp.UploadSHA256 {', PR),
    ('R26 canonical preview not bounded by the raw preview', VTR, 'lim.PreviewRows = min(lim.PreviewRows, len(preview))', 'lim.PreviewRows = lim.PreviewRows', PR),
    ('R27 row count not compared with the profile', VTR, 'if fail == nil && res.Rows != imp.TotalRows {', 'if false {', PR),
    # Round 1's fixes.
    ('N1 the confidence is not the server\'s (M1)', MAP, 'return e.RequiresConfirmation || e.Confidence < ConfirmBelow || e.TargetField == nil',
     'return e.RequiresConfirmation || e.TargetField == nil', PM),
    ('N2 a null target needs nobody (M2)', MAP, 'return e.RequiresConfirmation || e.Confidence < ConfirmBelow || e.TargetField == nil',
     'return e.RequiresConfirmation || e.Confidence < ConfirmBelow', PM),
    ('N3 a column left out by not listing it needs nobody (M2)', MAP,
     'if se.NeedsPerson() && se.TargetField == nil && !listed[se.SourceColumn] {', 'if false {', PM),
    ('N4 a null entry is not taken as the null suggestion (M2)', MAP,
     'left := se.TargetField == nil && e.TargetField == nil', 'left := false', PM),
    ('N5 offsets beyond 23:59 accepted (M3)', PRO, '([Zz]|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$', '([Zz]|[+-][0-9]{2}:[0-9]{2})$', PP),
    ('N6 "-00:00" taken as an offset (L6)', PRO, '!timestampText.MatchString(s) || strings.HasSuffix(s, "-00:00")', '!timestampText.MatchString(s)', PP),
    ('N7 the schema is not consulted in Validate (M3)', VAL,
     '\t\tif !canonical(data) {\n\t\t\tout.InvalidRows++', '\t\tif false && !canonical(data) {\n\t\t\tout.InvalidRows++', PV),
    ('N8 the schema is not consulted in Write (M3)', VAL,
     '\t\tif !canonical(data) {\n\t\t\treturn ErrChanged', '\t\tif false && !canonical(data) {\n\t\t\treturn ErrChanged', PV),
    ('N9 the stored suggestion is not checked against its schema (M1)', UPL,
     'if issues, err := validate.Document(i.MappingSuggestion, "ai-mapping.schema.json", CodeSuggestionInvalid); err != nil || len(issues) > 0 {',
     'if issues, err := validate.Document(i.MappingSuggestion, "ai-mapping.schema.json", CodeSuggestionInvalid); false && (err != nil || len(issues) > 0) {', PU),
    ('N10 IMPORT_INPUT_INVALID shown as the file\'s problem (L3)', 'internal/import/upload/resource.go',
     'case f.Code != store.InputInvalid && (strings.HasPrefix(f.Code, "IMPORT_")',
     'case true && (strings.HasPrefix(f.Code, "IMPORT_")', PU),
    ('N11 no checkpoint before the second read (L5)', VTR,
     'if ok, err := still(ctx); err != nil {', 'if ok, err := true, error(nil); err != nil {', PR),
]

os.makedirs(S, exist_ok=True)
lines = []
bad = 0
for i, (name, rel, old, new, pkg) in enumerate(M, 1):
    path = os.path.join(W, rel)
    src = open(path, encoding='utf-8').read()
    n = src.count(old)
    if n != 1:
        lines.append(f'{name}: NOT APPLIED ({n} matches of the original text)')
        bad += 1
        continue
    mutated = os.path.join(S, f'm{i}.go')
    open(mutated, 'w', encoding='utf-8').write(src.replace(old, new, 1))
    overlay = os.path.join(S, f'm{i}.json')
    json.dump({'Replace': {path: mutated}}, open(overlay, 'w'))
    r = subprocess.run(['go', 'test', '-count=1', '-overlay=' + overlay, pkg], cwd=W, capture_output=True, text=True, timeout=1800)
    fails = [l for l in r.stdout.splitlines() if l.startswith('--- FAIL') or 'build failed' in l or l.startswith('FAIL\t')]
    if r.returncode == 0:
        bad += 1
        lines.append(f'{name} [{rel}]: SURVIVED (tests passed)')
    else:
        lines.append(f'{name} [{rel}]: KILLED (exit {r.returncode}): ' + '; '.join(fails[:4]))
lines.append(f'{len(M)} mutations, {len(M) - bad} killed, {bad} survived or not applied')
lines.append('Method: each mutation is a one-line source change given to go test with -overlay; the worktree was never modified.')
open(OUT, 'w', encoding='utf-8').write('\n'.join(lines) + '\n')
print('\n'.join(lines))
sys.exit(1 if bad else 0)
