"""Limited semantic checks for specification fixtures, NOT the production Go validator.

These checks intentionally do not implement authorization, database access, dataset hash
verification, full JSON Patch execution, DES, all domain semantics or runtime budgets.
"""
from __future__ import annotations
from collections import defaultdict
from typing import Any


def lint_model(model: dict[str, Any]) -> list[str]:
    issues: set[str] = set()
    catalogs = [model['nodes'], model['resources'], model['flows'], model['entity_types'], model['metrics']]
    for items in catalogs:
        ids = [item['id'] for item in items]
        if len(ids) != len(set(ids)):
            issues.add('DUPLICATE_ID')
    nodes = {n['id']: n for n in model['nodes']}
    resources = {r['id']: r for r in model['resources']}
    if set(nodes) & set(resources):
        issues.add('DUPLICATE_ID')
    entity_types = {e['id'] for e in model['entity_types']}
    outgoing: dict[str, list[dict[str, Any]]] = defaultdict(list)
    graph: dict[str, list[str]] = defaultdict(list)
    for f in model['flows']:
        if f['from'] not in nodes or f['to'] not in nodes:
            issues.add('UNKNOWN_REFERENCE')
        outgoing[f['from']].append(f)
        if f['routing']['probability'] > 0:
            graph[f['from']].append(f['to'])
    zero_nodes: set[str] = set()
    sources = []
    for node in model['nodes']:
        ty = node['type']; nid = node['id']
        fs = outgoing[nid]
        if ty == 'sink':
            if fs: issues.add('ROUTING_INVALID')
        elif ty == 'decision':
            if not fs or abs(sum(f['routing']['probability'] for f in fs)-1) > 1e-9:
                issues.add('ROUTING_INVALID')
        elif len(fs) != 1 or abs(fs[0]['routing']['probability']-1) > 1e-9:
            issues.add('ROUTING_INVALID')
        if ty == 'source':
            sources.append(nid)
            if node['entity_type'] not in entity_types: issues.add('UNKNOWN_REFERENCE')
        if ty == 'queue':
            if len(fs) != 1 or nodes.get(fs[0]['to'],{}).get('type') != 'process':
                issues.add('ROUTING_INVALID')
        required_resources = []
        for required in node.get('requires',[]):
            rid = required['resource_id']; required_resources.append(rid)
            if rid not in resources: issues.add('UNKNOWN_REFERENCE')
            elif required['units'] > resources[rid]['capacity']: issues.add('RESOURCE_REQUIREMENT_INVALID')
        if len(required_resources) != len(set(required_resources)):
            issues.add('RESOURCE_REQUIREMENT_INVALID')
        if ty in ('queue','decision','failure','rework','sink'):
            zero_nodes.add(nid)
        for key in ('arrival','service_time','delay_time'):
            if key not in node: continue
            d = node[key]['distribution']
            if d['type'] == 'triangular' and not (d['min'] <= d['mode'] <= d['max']):
                issues.add('DISTRIBUTION_INVALID')
            if ty == 'source' and ((d['type']=='constant' and d['value']<=0) or
                                   (d['type']=='triangular' and d['min']<=0)):
                issues.add('DISTRIBUTION_INVALID')
            if ty != 'source' and ((d['type']=='constant' and d['value']==0) or
                                   (d['type']=='triangular' and d['min']==0)):
                zero_nodes.add(nid)
    for metric in model['metrics']:
        for key,catalog in [('resource_id',resources),('node_id',nodes),('sink_id',nodes)]:
            if key in metric and metric[key] not in catalog: issues.add('UNKNOWN_REFERENCE')
    for c in model['constraints']:
        if c['node_id'] not in nodes: issues.add('UNKNOWN_REFERENCE')
    for nid in model['views']['2d'].get('positions',{}):
        if nid not in nodes: issues.add('VIEW_REFERENCE_INVALID')
    if not sources: issues.add('NO_REACHABLE_SINK')
    reachable: set[str] = set()
    for source in sources:
        seen: set[str] = set(); stack = [source]
        while stack:
            n = stack.pop()
            if n in seen: continue
            seen.add(n); stack.extend(graph[n])
        reachable |= seen
        if not any(nodes.get(n,{}).get('type')=='sink' for n in seen):
            issues.add('NO_REACHABLE_SINK')
    visiting: set[str] = set(); done: set[str] = set()
    def cycle(n: str) -> bool:
        if n in visiting: return True
        if n in done: return False
        visiting.add(n)
        found = any(cycle(m) for m in graph[n] if m in zero_nodes and m in reachable)
        visiting.remove(n); done.add(n)
        return found
    if any(cycle(n) for n in sorted(zero_nodes & reachable) if n not in done):
        issues.add('ZERO_TIME_CYCLE')
    return sorted(issues)
