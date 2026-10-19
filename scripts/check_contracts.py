#!/usr/bin/env python3
"""Validate local JSON schemas, fixtures and OpenAPI structure/reference integrity.

No network access is used. This is not a replacement for an independent complete
OpenAPI validator, a Go semantic validator or API integration tests.
"""
from __future__ import annotations
import json
import re
import sys
from pathlib import Path
from typing import Any
from urllib.parse import urldefrag, urlparse
import yaml
from jsonschema import Draft202012Validator, FormatChecker
from referencing import Registry, Resource
from spec_fixture_lint import lint_model

ROOT = Path(__file__).resolve().parents[1]
DIALECT = 'https://json-schema.org/draft/2020-12/schema'


def reject_constant(value: str) -> None:
    raise ValueError(f'Non-finite JSON value: {value}')


def unique_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    out = {}
    for key, value in pairs:
        if key in out: raise ValueError(f'Duplicate JSON key: {key}')
        out[key] = value
    return out


def load(path: Path) -> Any:
    return json.loads(path.read_text(encoding='utf-8'), object_pairs_hook=unique_object,
                      parse_constant=reject_constant)


class UniqueKeyLoader(yaml.SafeLoader):
    """safe_load that refuses duplicate mapping keys (PyYAML keeps the last one silently)."""


def _unique_mapping(loader: UniqueKeyLoader, node: yaml.MappingNode, deep: bool = False) -> Any:
    seen = set()
    for key_node, _ in node.value:
        key = loader.construct_object(key_node, deep=deep)
        if key in seen: raise ValueError(f'Duplicate YAML key {key!r} at line {key_node.start_mark.line + 1}')
        seen.add(key)
    return yaml.SafeLoader.construct_mapping(loader, node, deep)


UniqueKeyLoader.add_constructor(yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, _unique_mapping)


def pointer(value: Any, fragment: str) -> Any:
    if not fragment: return value
    if not fragment.startswith('/'): raise ValueError(f'Unsupported anchor #{fragment}')
    for raw in fragment[1:].split('/'):
        key = raw.replace('~1','/').replace('~0','~')
        value = value[int(key)] if isinstance(value,list) else value[key]
    return value


def references(value: Any):
    if isinstance(value,dict):
        if '$ref' in value: yield value['$ref']
        for v in value.values(): yield from references(v)
    elif isinstance(value,list):
        for v in value: yield from references(v)


def error_pointers(errors) -> set[str]:
    """JSON Pointers of every error, including nested anyOf/oneOf context."""
    out, stack = set(), list(errors)
    while stack:
        e = stack.pop()
        out.add(''.join('/' + str(p).replace('~', '~0').replace('/', '~1') for p in e.absolute_path))
        stack.extend(e.context or [])
    return out


def formats(value: Any):
    if isinstance(value,dict):
        if isinstance(value.get('format'),str): yield value['format']
        for v in value.values(): yield from formats(v)
    elif isinstance(value,list):
        for v in value: yield from formats(v)


def main() -> dict[str, Any]:
    paths = sorted((ROOT/'schemas').glob('*.schema.json'))
    docs = {p:load(p) for p in paths}
    ids = {doc['$id']:p for p,doc in docs.items()}
    if len(ids) != len(docs): raise AssertionError('Duplicate schema $id')
    # jsonschema silently skips formats whose optional checker package is missing.
    used_formats = {f for doc in docs.values() for f in formats(doc)}
    unchecked = used_formats - set(FormatChecker().checkers)
    if unchecked:
        raise AssertionError(f'No format checker installed for {sorted(unchecked)}; '
                             'install requirements-spec.txt')
    pairs = []
    for path,doc in docs.items():
        Draft202012Validator.check_schema(doc)
        resource = Resource.from_contents(doc)
        pairs.extend([(doc['$id'],resource),(path.as_uri(),resource)])
    registry = Registry().with_resources(pairs)
    oas_path = ROOT/'openapi/openapi.yaml'
    oas = yaml.load(oas_path.read_text(encoding='utf-8'), Loader=UniqueKeyLoader)
    docs[oas_path] = oas
    ref_count = 0
    for path,doc in list(docs.items()):
        for value in references(doc):
            target,frag = urldefrag(value)
            if not target: target_path = path
            elif target in ids: target_path = ids[target]
            elif urlparse(target).scheme:
                raise AssertionError(f'Unexpected network $ref: {value}')
            else: target_path = (path.parent/target).resolve()
            if target_path not in docs:
                raise AssertionError(f'Broken external $ref in {path.name}: {value}')
            pointer(docs[target_path],frag)
            ref_count += 1
    def validator(spec: str) -> Draft202012Validator:
        part,frag = urldefrag(spec)
        schema = load(ROOT/part)
        uri = schema['$id'] + ('#'+frag if frag else '')
        return Draft202012Validator({'$schema':DIALECT,'$ref':uri},registry=registry,
                                    format_checker=FormatChecker())
    cases = load(ROOT/'testdata/contracts/cases.json')['cases']
    positive = negative = semantic = exact_paths = 0
    for case in cases:
        instance = load(ROOT/case['instance'])
        errors = list(validator(case['schema']).iter_errors(instance))
        if case['layer']=='schema':
            if bool(errors) == case['valid']:
                detail = '\n'.join(f'{e.json_path}: {e.message}' for e in errors[:3])
                raise AssertionError(f"Unexpected outcome for {case['id']}\n{detail}")
            # TEST-001: a negative must fail at the field it was written for.
            missing = set(case.get('expected_error_paths', [])) - error_pointers(errors)
            if missing:
                raise AssertionError(f"{case['id']}: no error at {sorted(missing)}; "
                                     f"got {sorted(error_pointers(errors))}")
            positive += case['valid']; negative += not case['valid']
            exact_paths += bool(case.get('expected_error_paths'))
        else:
            if errors: raise AssertionError(f"Semantic fixture must first be schema-valid: {case['id']}")
            issues = lint_model(instance)
            if not set(case['expected_codes']).issubset(issues):
                raise AssertionError(f"{case['id']}: expected {case['expected_codes']}; got {issues}")
            semantic += 1
    # Schema-positive complete fixtures should also pass the LIMITED model lint.
    for path in [ROOT/'testdata/golden-models'/n for n in ['default-delivery.json','report-example.json','deterministic-queue.json','congested-queue.json']]+[ROOT/'testdata/contracts/valid-minimal-ir.json']:
        name=path.name; issues=lint_model(load(path))
        if issues: raise AssertionError(f'{name}: {issues}')
    assert oas['openapi']=='3.1.1'
    assert oas['jsonSchemaDialect']==DIALECT
    assert oas['security']==[{'bearerAuth':[]}]
    operations=[]
    for path,item in oas['paths'].items():
        for method,operation in item.items():
            assert method in {'get','post','put','patch','delete','options','head','trace'}
            operations.append(operation['operationId'])
            assert operation['x-origin'] in {'SOURCE','PROPOSED'}
            roles=operation['x-required-roles']
            assert set(roles) <= {'owner','editor','viewer'} and len(roles)==len(set(roles)),(method,path)
            resolved=[]
            for p in operation['parameters']:
                resolved.append(pointer(oas,p['$ref'][1:]) if '$ref' in p else p)
            path_vars=set(re.findall(r'\{([^}]+)\}',path))
            assert path_vars=={p['name'] for p in resolved if p['in']=='path'}
            assert all(p.get('required') for p in resolved if p['in']=='path')
            # An operation runs in one organization (the default): X-Organization-ID
            # is required and a role of that membership authorizes it. Or, with
            # x-tenant-scope user, it reads the caller's own identity (D-045): no
            # organization, so no role, and the header is declared as one that must
            # not be sent (no value is valid) and is refused with 400 (the server
            # never ignores it); an unknown identity is 403.
            scope=operation.get('x-tenant-scope','organization')
            assert scope in {'organization','user'},(method,path,scope)
            selectors=[p for p in resolved if p['name']=='X-Organization-ID']
            assert len(selectors)==1 and selectors[0]['in']=='header',(method,path)
            if scope=='organization':
                assert roles and selectors[0]['required'],(method,path)
            else:
                assert roles==[] and not selectors[0].get('required') and selectors[0]['schema']=={'not':{}},(method,path)
                assert {'400','401','403'} <= set(operation['responses']),(method,path)
                assert not re.search(r'\{[^}]*organization',path),(method,path)
            assert operation['responses']
    assert len(operations)==len(set(operations))
    # D-025: every error code maps to exactly one documented error response,
    # each response lists exactly its codes, and every request body may be 413
    # and 415 (ADR-019: the shared body size and media type rules).
    status_of=docs[ROOT/'schemas/common.schema.json']['$defs']['ErrorCode']['x-http-status']
    assert set(status_of)==set(docs[ROOT/'schemas/common.schema.json']['$defs']['ErrorCode']['enum'])
    error_responses={int(k[5:]):v for k,v in oas['components']['responses'].items() if re.fullmatch(r'Error\d{3}',k)}
    assert set(status_of.values())==set(error_responses),(sorted(set(status_of.values())),sorted(error_responses))
    for status,resp in error_responses.items():
        listed=re.search(r'Codes: (.*)\.$',resp['description']).group(1).split(', ')
        assert sorted(listed)==sorted(c for c,s in status_of.items() if s==status),status
    for path,item in oas['paths'].items():
        for method,operation in item.items():
            if 'requestBody' in operation:
                assert '413' in operation['responses'] and '415' in operation['responses'],(method,path)
            for code,resp in operation['responses'].items():
                if code[0] in '45':
                    assert int(code) in error_responses,(method,path,code)
                    # An operation's own error response lists only codes of its status.
                    if '$ref' not in resp:
                        listed=re.search(r'Codes: (.*)\.$',resp['description']).group(1).split(', ')
                        assert listed and all(status_of.get(c)==int(code) for c in listed),(method,path,code)
                        assert resp['content']['application/json']['schema']=={'$ref':'../schemas/common.schema.json#/$defs/Error'},(method,path,code)
                # Inline examples match the response's schema.
                for media in resp.get('content',{}).values():
                    for name,example in media.get('examples',{}).items():
                        ref=media['schema']['$ref']
                        assert ref.startswith('../schemas/'),(method,path,code,ref)
                        errors=list(validator(ref[len('../'):]).iter_errors(example['value']))
                        assert not errors,(method,path,code,name,[e.message for e in errors])
    inventory=load(ROOT/'planning/api-operations.json')['operations']
    assert set(operations)=={x['operation_id'] for x in inventory}
    assert len(inventory)==len(operations)
    # The inventory the Go and TypeScript route tests read is the contract's:
    # method, path, origin, roles and tenant scope of each operation.
    by_id={x['operation_id']:x for x in inventory}
    for path,item in oas['paths'].items():
        for method,operation in item.items():
            x=by_id[operation['operationId']]
            assert set(x) <= {'method','path','operation_id','origin','roles','scope'},x
            assert (x['method'],x['path'],x['origin'],x['roles'],x.get('scope','organization'))==\
                (method.upper(),'/v1'+path,operation['x-origin'],operation['x-required-roles'],
                 operation.get('x-tenant-scope','organization')),operation['operationId']
    # Optional installed parser adds a structural model check; not full OAS conformance.
    fastapi_status='NOT_RUN_NOT_INSTALLED'
    try:
        from fastapi.openapi.models import OpenAPI
    except ImportError:
        pass
    else:
        OpenAPI.model_validate(oas)
        fastapi_status='PASS_STRUCTURAL_MODEL_ONLY'
    corpus={}
    for path in sorted((ROOT/'testdata/ai-evals').glob('*.jsonl')):
        items=[json.loads(line) for line in path.read_text(encoding='utf-8').splitlines() if line.strip()]
        assert len(items)==len({i['id'] for i in items}),path.name
        corpus[path.stem]=len(items)
    assert corpus['modeler']>=50
    report={'status':'PASS_LIMITED_SPEC_CHECKS','schema_metaschemas':len(paths),
            'schema_positive_fixtures':positive,'schema_negative_fixtures':negative,
            'negative_fixtures_with_exact_error_path':exact_paths,
            'limited_semantic_negative_fixtures':semantic,'resolved_local_ref_occurrences':ref_count,
            'openapi_operations':len(operations),'optional_fastapi_openapi_model':fastapi_status,
            'independent_complete_openapi_validator':'NOT_RUN_UNAVAILABLE',
            'ai_corpus_records':corpus,'live_ai_eval':'NOT_RUN','go_engine_tests':'NOT_RUN'}
    (ROOT/'checks/contracts-check.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
    print(json.dumps(report,ensure_ascii=False,indent=2))
    return report

if __name__=='__main__':
    try: main()
    except Exception as exc:
        print(f'CONTRACT CHECK FAILED: {exc}',file=sys.stderr)
        raise
