import json, pathlib
b = '/tmp/claude-0/-home-user-Geminus/b4164204-ed28-580b-a7b5-c02b59026d38/scratchpad/'
a = pathlib.Path(b + 'm/internal/results/metrics/aggregate.go').read_text()
old = '\t\tc.endSetups(c.h) // a later end is cut at H by the integrals\n'
assert a.count(old) == 1
pathlib.Path(b + 'probes/aggregate-no-finish-ends.go').write_text(a.replace(old, ''))
q = {b + 'm/internal/simulation/des/zz_review_probe6_test.go': b + 'probes/zz_review_probe6_test.go'}
json.dump({'Replace': q}, open(b + 'probes/overlay-p6.json', 'w'))
qm = dict(q)
qm[b + 'm/internal/results/metrics/aggregate.go'] = b + 'probes/aggregate-no-finish-ends.go'
json.dump({'Replace': qm}, open(b + 'probes/overlay-p6-mutant.json', 'w'))
print('ok')
