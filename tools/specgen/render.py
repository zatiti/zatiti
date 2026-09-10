#!/usr/bin/env python3
"""Render all self-contained scope prompts; --check detects stale outputs.

Authoritative input is local model.py/packages.py and docs/implementation sources.
No RFC, model, network, coding harness or third-party Python library is required.
"""
import argparse
import hashlib
import json
from pathlib import Path
import sys
from model import D, OPS, DEFINITION_TYPES
from packages import P

ROOT=Path(__file__).resolve().parents[2]
DOC=ROOT/'docs/implementation'

def dump(x): return json.dumps(x,ensure_ascii=False,indent=2)+'\n'
def compact(x): return json.dumps(x,ensure_ascii=False,separators=(',',':'))
def refs(x):
    if isinstance(x,dict):
        for k,v in x.items():
            if k=='$ref': yield v
            else: yield from refs(v)
    elif isinstance(x,list):
        for v in x: yield from refs(v)

def closure(schemas,definitions):
    result={}; pending=list(refs(schemas))
    while pending:
        r=pending.pop()
        if not r.startswith('#/$defs/'): raise ValueError('non-local schema reference '+r)
        n=r[len('#/$defs/'):]
        if n not in definitions: raise ValueError('unresolved schema reference '+r)
        if n not in result:
            result[n]=definitions[n]; pending.extend(refs(definitions[n]))
    return dict(sorted(result.items()))

def schemas_md(ops,definitions):
    if not ops: return 'No operation handlers are owned or called by this scope. Its Go interfaces are specified above.\n'
    chunks=[]
    for o in sorted(ops,key=lambda x:x['id']):
        mapping=('CLI `zatiti '+' '.join(o['cli'])+'`; MCP `'+o['mcp']+'`.') if o['visibility']=='public' else ('Allowed internal callers: '+', '.join(o['callers'])+'.')
        chunks.append(f"### `{o['id']}` v1 — {o['owner']} / {o['visibility']} / {o['mode']} / {o['effect']}\n\n{mapping} Submission key: {'required' if o['submission_key'] else 'not required at this internal/query/bootstrap boundary'}.\n\n{o['behavior']}\n\nInput schema:\n```json\n{compact(o['input_schema'])}\n```\nOutput data schema:\n```json\n{compact(o['output_schema'])}\n```\n")
    
        if 'completion_schema' in o:
            chunks.append('Eventual job result schema (job.get resource.result):\n```json\n'+compact(o['completion_schema'])+'\n```\n')
    defs=closure([{'input':o['input_schema'],'output':o['output_schema'],'completion':o.get('completion_schema',{})} for o in ops],definitions)
    chunks.append('### Local schema definitions\n\nThe schemas above resolve exclusively against this embedded `$defs` object. Input objects reject additional properties except explicitly open schema/data fields. Field semantics are completed by the owned requirements and operation descriptions. Output `resource`, `items`, `draft`, `job`, etc. are literal keys. Pagination cursor lives in the common envelope.\n\n```json\n'+compact({'$defs':defs})+'\n```\n')
    return '\n'.join(chunks)

def validate(requirements,acceptance,adapter):
    names={p['name'] for p in P}; paths=[p['path'] for p in P]
    assert len(names)==len(P) and len(paths)==len(set(paths)), 'duplicate package/root'
    for a in paths:
        for b in paths:
            assert a==b or not b.startswith(a+'/'), f'overlapping implementation roots: {a}, {b}'
    ids=[o['id'] for o in OPS]; assert len(ids)==len(set(ids)), 'duplicate operation'
    for key in ['mcp','cli']:
        vals=[compact(o[key]) for o in OPS if o['visibility']=='public']
        assert len(vals)==len(set(vals)), 'mapping collision '+key
    for o in OPS:
        assert o['owner'] in names, o['id']
        assert set(o['callers']) <= names, (o['id'],o['callers'])
        assert o['visibility']!='internal' or o['callers'], o['id']
        closure([o['input_schema'],o['output_schema'],o.get('completion_schema',{})],D)
    for r in requirements:
        assert r['owner'] in names and set(r['participants']) <= names, r['id']
        assert r['text'].strip(),r['id']
    a_ids=[x['id'] for x in acceptance]; assert len(a_ids)==len(set(a_ids))
    assert {f'Z{i:02}' for i in range(1,22)} <= {a['gate'] for a in acceptance}
    for a in acceptance:
        assert a['owners'] and set(a['owners']) <= names,a['id']
        assert a['setup'] and a['action'] and a['expected'],a['id']
    state={}
    def visit(n):
        assert state.get(n)!=1, 'Go import cycle at '+n
        if state.get(n)==2:return
        state[n]=1
        p=next(p for p in P if p['name']==n)
        assert set(p['imports'])<=names,(n,p['imports'])
        for d in p['imports']:visit(d)
        state[n]=2
    for n in names:visit(n)
    closure(list(adapter['definitions'].values()),adapter['definitions'])
    for n,m in adapter['adapter_mapping'].items():
        assert n in names
        for value in m.values():
            if isinstance(value,str): assert value in adapter['definitions'],(n,value)
    # Optional additional standards validation, never needed to render a fresh clone.
    try:
        from jsonschema import Draft202012Validator
    except ImportError:
        return 'structural validation (optional jsonschema library unavailable)'
    for n,s in D.items(): Draft202012Validator.check_schema(s)
    for o in OPS:
        for key in ('input_schema','output_schema','completion_schema'):
            if key in o: Draft202012Validator.check_schema(o[key])
    for n,s in adapter['definitions'].items(): Draft202012Validator.check_schema(s)
    return 'JSON Schema 2020-12 plus structural validation'

def main():
    ap=argparse.ArgumentParser();ap.add_argument('--check',action='store_true');ap.add_argument('--output-dir',type=Path,default=ROOT)
    args=ap.parse_args()
    requirements=json.loads((DOC/'requirements.json').read_text())
    acceptance=json.loads((DOC/'acceptance.json').read_text())
    adapter=json.loads((DOC/'adapter-schemas.json').read_text())
    common=(DOC/'contracts.md').read_text()
    validation=validate(requirements,acceptance,adapter)
    fingerprint=hashlib.sha256(compact([P,D,OPS,requirements,acceptance,adapter,common]).encode()).hexdigest()
    lookup={p['name']:p for p in P}
    outputs={}
    outputs['docs/implementation/operations.json']=dump({'revision':1,'$defs':D,'definition_kind_map':DEFINITION_TYPES,'operations':OPS})
    outputs['docs/implementation/packages.json']=dump({'revision':1,'packages':P})
    coverage=[]
    for r in requirements:
        coverage.append({'requirement':r['id'],'section':r['section'],'kind':r['kind'],'owner':lookup[r['owner']]['path'],'participants':[lookup[x]['path'] for x in r['participants']],'verification':['tests/integration','tests/qualification']})
    outputs['docs/implementation/coverage.json']=dump({'revision':1,'requirements':coverage,'acceptance_cases':[{'id':a['id'],'gate':a['gate'],'scopes':[lookup[x]['path'] for x in a['owners']]} for a in acceptance]})
    consumers={'registry','application','server','cli','mcp','desktop','integration','qualification','zatiti'}
    for p in P:
        n=p['name']; owned=[o for o in OPS if o['owner']==n]
        outgoing=[o for o in OPS if n in o['callers'] and o['owner']!=n]
        available=owned+outgoing
        if n in consumers:
            available += [o for o in OPS if o['visibility']=='public']
        available=list({o['id']:o for o in available}.values())
        selected=[r for r in requirements if n==r['owner'] or n in r['participants']]
        cases=[a for a in acceptance if n in a['owners'] or n in ('integration','qualification')]
        incoming=sorted({c for o in owned for c in o['callers']})
        if any(o['visibility']=='public' for o in owned):incoming+=['application (authenticated public operations)']
        imports=', '.join('`github.com/zatiti/zatiti/'+lookup[x]['path']+'`' for x in p['imports']) or 'standard library only; no product package imports'
        parts=[f"# Implementation assignment: `{p['path']}`\n\nGenerated specification revision 1; source digest `{fingerprint}`. This file is committed implementation context. Do not independently edit it. Everything required from the product specification and adjacent interfaces is embedded below; no RFC copy is required.\n\n## Mission and scope\n\n{p['mission']}\n\nWrite scope: **`{p['path']}/` only**, excluding this generated AGENTS.md. Go package name: `{('main' if p['kind']=='entrypoint' else 'integration_test' if n=='integration' else 'qualification_test' if n=='qualification' else n) if p['kind']!='support' else 'not a Go package'}`. Ownership kind: {p['kind']}; integration wave: {p['wave']}.\n\nAllowed production imports from this repository: {imports}. Tests may use interfaces/fakes defined locally and, once available, storage-backed temporary fixtures; integration owns cross-package tests. No sibling raw SQL. No root dependency edits except the integration exception stated in its own brief.\n\n## Implementation decisions and acceptance focus\n\n{p['design']}\n\nLocal proving focus: {p['tests']}\n\n## Incoming and outgoing boundaries\n\nIncoming callers: {', '.join(incoming) or 'entrypoint/assembly or tests via the explicit Go API'}.\n\nOutgoing owner calls: {', '.join('`'+o['id']+'`' for o in outgoing) or 'none; use only declared Go dependency interfaces'}. Each exact input/output schema appears below. Calls retain current Unit/authority; owner allowlists are mandatory.\n"]
        if p['kind']=='domain':
            parts.append('Expose `New(contract.Dependencies) (*Service,error)`; `*Service` implements `contract.Module` with Name `'+n+'`, owner-prefixed migrations, all owned descriptors, and strict dispatch. No calls/goroutines during construction. Implement optional authentication/LocalIO interfaces where specified in the common contract. Tables are private under `'+n+'_`; external callers rely only on methods and schemas.\n')
        elif p['kind']=='adapter':
            parts.append('Expose `New(contract.AdapterDependencies,json.RawMessage) (contract.Adapter,error)`. The profile/action/evidence schemas below are the local frozen seam. Upstream translation must use the qualified pinned public API; one Invoke means one accounted physical call. No arbitrary model-supplied endpoint/account.\n')
        if p['imports']:
            parts.append('### Imported package APIs and behavior\n\nThese briefs are embedded so you need not read a sibling prompt to discover its incoming API. Implement only your own package.\n')
            for d in p['imports']:
                if d=='contract':continue
                dep=lookup[d]
                parts.append(f"**`{dep['path']}`** — {dep['mission']}\n\n{dep['design']}\n")
        parts.append('## Shared foundation contract\n\n'+common)
        parts.append('## Owned product requirements\n\n'+('\n\n'.join(f"### {r['id']} (source section {r['section']}; primary owner {r['owner']})\n\n{r['text']}" for r in selected) or 'This foundation/support scope fulfills the shared contract and the specific ownership/acceptance brief above.'))
        parts.append('## Exact operation and dependency schemas\n\n'+schemas_md(available,D))
        if n in {'responses','github','httpread','serenity','execution','effects','controller','memory','installation','artifacts','connections','tasks','integration','qualification','zatiti'}:
            parts.append('## Local adapter, context, verifier and backup payloads\n\nThese local schemas freeze the handoff between execution, adapters and artifact publication. They do not assert upstream compatibility.\n\n'+ '\n'.join(adapter.get('notes',[]))+'\n\n```json\n'+compact({'$defs':adapter['definitions'],'adapter_mapping':adapter['adapter_mapping']})+'\n```\n')
        parts.append('## Named acceptance cases\n\nTests are implementation deliverables, not claims of already executed qualification. Retain expected/observed results, exact source/config/tool versions and failure evidence.\n')
        for a in cases:
            expected=a['expected'] if isinstance(a['expected'],list) else [a['expected']]
            parts.append(f"### {a['id']} — {a['gate']}\n\nSetup: {a['setup']}\n\nAction: {a['action']}\n\nExpected:\n\n"+'\n'.join('- '+x for x in expected)+'\n')
        if not cases:parts.append(p['tests']+'\n')
        parts.append('## Delivery\n\nImplement production behavior and meaningful local tests within scope. Report files changed, commands actually run, observed results, unresolved dependency qualifications and contract defects. A missing dependency may use an exact local fake for development; production must return a named prerequisite/unsupported error instead of fake success. Integration owns shared dependency changes, real assembly, cross-package proving and landing. Do not advertise release or client/platform support from compilation alone.\n')
        outputs[p['path']+'/AGENTS.md']='\n'.join(parts)
    rows=['| Scope | Kind | Wave | Ownership |','|---|---|---:|---|']
    for p in P:rows.append(f"| [`{p['path']}`](../../{p['path']}/AGENTS.md) | {p['kind']} | {p['wave']} | {p['mission']} |")
    outputs['docs/implementation/README.md']=f'''# Package implementation specification

Revision 1. **Specification scaffold; no product implementation or executed release qualification is claimed.**

{len(P)} disjoint implementation roots; {sum(o['visibility']=='public' for o in OPS)} public operations; {sum(o['visibility']=='internal' for o in OPS)} internal owner methods; {len(requirements)} source blocks with explicit ownership; {len(acceptance)} named acceptance cases covering Z01–Z21 and release journeys.

Each root already contains a complete committed AGENTS.md: local mission, allowed imports, owned requirements, exact Go interfaces, incoming/outgoing operation schemas, persistence/recovery rules and named acceptance criteria. An agent can implement from that file without the RFC. Scope prompts intentionally repeat necessary contracts; do not edit generated copies independently.

## Start and dispatch

1. Assign the integration owner its **serialized foundation assignment**: create root go.mod/go.sum for module github.com/zatiti/zatiti, Go 1.26.0/toolchain 1.26.2, resolve exact dependency releases/commits/licenses/checksums, and qualify required upstream seams. Record docs/implementation/dependencies.lock.json. This is a specified implementation deliverable, not a preexisting lock or compatibility claim.
2. Implement wave 0 contract/platform/storage against frozen interfaces. A dependency library required by a scope must be pinned before that scope starts. Land the shared contract baseline before dispatching dependent agents; no guessing or independently rewriting shared types.
3. Wave 1 establishes identity/compiler and transport/application foundations. Wave 2 domain services, adapters and presentation transports can generate code concurrently against the baseline, using exact local fakes. The wave numbers are integration order, not a claim that all runtime collaborators already exist.
4. Wave 3 assembles real controller, entrypoints and desktop; replace development fakes with actual modules. Wave 4 proves cross-package transactions, all operations/parity/journeys and real platform/client/provider qualification.
5. Use one isolated worktree per concurrent root and one landing owner. Dispatch only disjoint roots; no ancestor/root assignment concurrently with descendants. A package may add private files in its root but no new ownership roots or exported seams without a coordinated revision.

Root dependency work and the lock report belong only to integration's serialized exception. cmd entrypoints own wiring, tests/integration owns product-wide fixtures, tests/qualification owns external/GUI qualification, packaging owns distribution, and .github/workflows owns CI. The root AGENTS.md is stable repository guidance, not a concurrently implementable root task. There is no Kazi/apply dependency and no assumption about coding harness.

## Frozen ownership

{chr(10).join(rows)}

## Sources and validation

- [Shared contracts](contracts.md): exact Go boundary, transactions, wire/error/replay/IO rules and selected implementation families.
- [Operation catalog](operations.json): every public/internal operation, strict JSON Schemas, owner, allowed callers, CLI/MCP mapping and behavior.
- [Adapter schemas](adapter-schemas.json): exact local context/model/tool/evidence/verifier/backup seams; upstream translation is explicitly qualified.
- [Requirements](requirements.json): retained full source requirement blocks, owner and participants. The renderer does not read docs/rfc.md.
- [Coverage](coverage.json): source block → implementation/verification roots and named-case ownership.
- [Acceptance](acceptance.json): named setups/actions/expected observations, not claims of tests already run.
- [Package manifest](packages.json): frozen roots, imports, missions and implementation decisions.

Authoritative edits: tools/specgen/model.py and packages.py, contracts.md, requirements.json, acceptance.json, adapter-schemas.json. Rendered: every package AGENTS.md, operations.json, packages.json, coverage.json and this index. The root AGENTS.md and ADR are authored stable guidance.

```sh
python3 tools/specgen/render.py
python3 tools/specgen/render.py --check
```

Renderer checks unique/disjoint roots, acyclic allowed Go imports, operation/schema ownership, public name collisions, internal caller allowlists, reference closure, requirement/case ownership, all Z01–Z21 gates and byte-for-byte prompt freshness. When Python jsonschema is available it also validates JSON Schema 2020-12 syntax. Structural checks do not prove semantic implementation correctness or replace real integration tests.

Regeneration can run with only the authoritative inputs above and no RFC. --output-dir DIR renders an independent tree for review. Contract changes update all copies together and require affected-dependency review; implementers cannot lower acceptance criteria.

## Qualification boundary

The RFC deliberately leaves external versions and protocol capability qualification open. This specification selects library families/framework/provider and freezes local interfaces; integration must resolve and test exact upstream pins before dependent implementation. In particular Serenity command reconciliation/cost enforcement and provider hard caps are requirements to establish, never invented capabilities. An unavailable required guarantee blocks enabling that mode and its release gate; it must not become fake success or an undocumented substitute.
'''
    bad=[]
    for name,content in sorted(outputs.items()):
        dest=args.output_dir/name
        if args.check:
            if not dest.exists() or dest.read_text()!=content:bad.append(name)
        else:
            dest.parent.mkdir(parents=True,exist_ok=True);dest.write_text(content)
    if bad:
        print('Stale/missing generated files:\n'+'\n'.join(bad));return 1
    print(f"{'Checked' if args.check else 'Rendered'} {len(outputs)} files; {len(P)} scopes, {len(OPS)} operations, {len(requirements)} source blocks, {len(acceptance)} cases; {validation}.")
    return 0
if __name__=='__main__':sys.exit(main())
