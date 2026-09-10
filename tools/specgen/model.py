"""Authoritative revision-1 operation schemas and ownership. Standard library only."""
from copy import deepcopy

S={'type':'string','maxLength':8192}
ID={'type':'string','format':'uuid'}
DIG={'type':'string','pattern':'^[0-9a-f]{64}$'}
TIME={'type':'string','format':'date-time'}
INT={'type':'integer','minimum':0,'maximum':9223372036854775807}
VER={'type':'integer','minimum':1,'maximum':9223372036854775807}
BOOL={'type':'boolean'}
JSON={'type':'object','description':'Inert JSON data bounded by the enclosing size limit; never executable authority.'}
def ref(n): return {'$ref':'#/$defs/'+n}
def arr(t, maximum=4096): return {'type':'array','items':deepcopy(t),'maxItems':maximum}
def enum(*v): return {'type':'string','enum':list(v)}
def obj(**fields):
    return {'type':'object','additionalProperties':False,'properties':{k.rstrip('?'):deepcopy(v) for k,v in fields.items()},'required':[k for k in fields if not k.endswith('?')]}
def fields(d): return obj(**d)
D={}
D['Scope']=obj(installation_id=ID, **{'organization_id?':ID,'project_id?':ID,'worker_id?':ID,'task_id?':ID})
D['Ref']=obj(id=ID,version=VER)
D['Money']=obj(currency={'type':'string','pattern':'^[A-Z]{3}$'},micro_units=INT)
D['Limits']=obj(currency={'type':'string','pattern':'^[A-Z]{3}$'},spend_micro_units=INT,concurrency=VER,model_steps=VER,child_count=INT,delegation_depth=INT,attempt_seconds=VER,root_deadline=TIME)
D['Usage']=obj(currency=S,spent=INT,reserved=INT,estimated=INT,unknown=INT,advisory=BOOL)
D['ArtifactRef']=obj(id=ID,digest=DIG)
D['Requirement']=obj(code=S,message=S,**{'resource_id?':ID,'challenge_id?':ID})
D['Diagnostic']=obj(path=S,code=S,message=S,severity=enum('error','warning','info'))
D['DecisionRequirement']=obj(action_digest=DIG,human_required=BOOL,eligible_principals=arr(ID),expires_at=TIME,separate_proposer=BOOL)
D['Rule']=obj(capability=S,effect=enum('local','disclosure','external_read','external_mutation'),destinations=arr(S),decision=enum('allow','deny','review'),human_required=BOOL,conditions=JSON)
D['Principal']=obj(id=ID,version=VER,kind=enum('human','client_agent','worker','service'),name=S,scope=ref('Scope'),revoked=BOOL)
D['Grant']=obj(id=ID,version=VER,principal_id=ID,scope=ref('Scope'),capabilities=arr(S),destinations=arr(S),denied=BOOL,**{'expires_at?':TIME,'parent_grant_id?':ID})
D['Credential']=obj(id=ID,version=VER,principal_id=ID,store_ref=S,revoked=BOOL,**{'expires_at?':TIME})
D['Organization']=obj(id=ID,version=VER,key=S,name=S,chief_id=ID,**{'parent_id?':ID,'limits?':ref('Limits'),'extensions?':JSON})
D['Team']=obj(id=ID,version=VER,organization_id=ID,key=S,name=S,worker_ids=arr(ID),**{'extensions?':JSON})
D['Project']=obj(id=ID,version=VER,organization_id=ID,key=S,name=S,repositories=arr(S),bindings=arr(ID),classification=enum('internal','public','restricted'),**{'limits?':ref('Limits'),'extensions?':JSON})
D['ExecutionProfile']=obj(id=ID,version=VER,executor=enum('hosted','cooperative'),model=S,connection_id=ID,provider_destination=S,capabilities=arr(S),cost_bound=ref('Money'),classification=enum('internal','public','restricted'),context_capture=enum('complete','partial','advisory'))
D['Worker']=obj(id=ID,version=VER,organization_id=ID,key=S,name=S,purpose=S,instructions=S,skill_versions=arr(ref('Ref')),bindings=arr(ID),profile=ref('ExecutionProfile'),limits=ref('Limits'),**{'extensions?':JSON})
D['Binding']=obj(id=ID,version=VER,scope=ref('Scope'),kind=enum('tool','skill','connection','worker','repository','reporting','memory'),target_id=ID,permissions=arr(S),**{'source_scope?':ref('Scope'),'destinations?':arr(S)})
D['Skill']=obj(id=ID,version=VER,name=S,instruction_artifact=ref('ArtifactRef'),content_digest=DIG,input_schema=JSON,output_schema=JSON,requirements=arr(S),dependencies=arr(ref('Ref')),source=S,license=S,evaluation_refs=arr(ID),diagnostics=arr(ref('Diagnostic')))
D['Tool']=obj(id=ID,version=VER,name=S,input_schema=JSON,output_schema=JSON,effect=enum('local','disclosure','external_read','external_mutation'),destinations=arr(S),credential_kind=S,cost_bound=ref('Money'),timeout_seconds=VER,idempotency=enum('none','qualified_key','authoritative_nonexecution'),key_retention_seconds=INT,confirmation=enum('synchronous','asynchronous','advisory'),reconciliation=S,adapter=S)
D['Connection']=obj(id=ID,version=VER,scope=ref('Scope'),provider=S,account_identity=S,credential_ref=S,destinations=arr(S),allowed_scopes=arr(S),validation_state=enum('unverified','valid','invalid','expired','revoked'),**{'validated_at?':TIME,'valid_until?':TIME})
D['Challenge']=obj(id=ID,version=VER,connection_id=ID,state=enum('pending','external_action_required','completed','cancelled','expired','failed'),expires_at=TIME,**{'consent_url?':S,'helper_ref?':S,'requirements?':arr(ref('Requirement'))})
D['Policy']=obj(id=ID,version=VER,scope=ref('Scope'),rules=arr(ref('Rule')),**{'extensions?':JSON})
D['PromotionRule']=obj(id=ID,version=VER,scope=ref('Scope'),capability=S,destinations=arr(S),required_evidence=arr(S),minimum_successes=VER,evidence_window_seconds=VER,disqualifying_events=arr(S),ceiling_grant_id=ID,human_required_preserved=BOOL)
D['Qualification']=obj(id=ID,version=VER,worker_id=ID,capability=S,destinations=arr(S),rule=ref('Ref'),model=S,tool_versions=arr(ref('Ref')),skill_versions=arr(ref('Ref')),evidence_ids=arr(ID),window_start=TIME,window_end=TIME,state=enum('proposed','qualified','rejected','restricted','expired'),explanation=S)
D['Acceptance']=obj(verifier_id=S,verifier_version=S,sealed_inputs=arr(ref('ArtifactRef')),expected_observations=JSON,mode=enum('independent','manual'),required_child_ids=arr(ID))
D['Task']=obj(id=ID,version=VER,scope=ref('Scope'),owner_id=ID,worker_id=ID,outcome=S,inputs=arr(ref('ArtifactRef')),required_outputs=arr(S),acceptance=ref('Acceptance'),limits=ref('Limits'),dependencies=arr(ID),state=enum('draft','ready','running','waiting','verifying','succeeded','failed','cancelled'),**{'parent_id?':ID,'root_id?':ID,'waiting_reason?':S,'cancellation_requested?':BOOL,'manual_acceptance?':BOOL})
D['Schedule']=obj(id=ID,version=VER,scope=ref('Scope'),task_template=ref('Task'),timezone=S,expression=S,misfire=enum('coalesce','skip'),catch_up_seconds=INT,paused=BOOL,**{'next_wake?':TIME})
D['Responsibility']=obj(id=ID,version=VER,scope=ref('Scope'),worker_id=ID,outcome=S,signals=arr(S),triggers=arr(S),reasoning_policy=S,min_interval_seconds=VER,cycle_limits=ref('Limits'),aggregate_limits=ref('Limits'),pause_conditions=arr(S),escalation_conditions=arr(S),acceptance=ref('Acceptance'),paused=BOOL,**{'next_wake?':TIME})
D['Run']=obj(id=ID,version=VER,task_id=ID,configuration_revision=VER,input_versions=arr(ref('Ref')),state=enum('ready','running','waiting','verifying','succeeded','failed','cancelled'),attempt_ids=arr(ID))
D['Attempt']=obj(id=ID,version=VER,run_id=ID,worker_id=ID,executor=enum('hosted','cooperative'),generation=VER,lease_id=ID,lease_expires_at=TIME,last_heartbeat=TIME,reservation_id=ID,state=enum('claimed','running','waiting','reported','fenced','stopped','failed'),capabilities=arr(S),**{'context_artifact?':ref('ArtifactRef'),'recovery_reason?':S})
D['Action']=obj(scope=ref('Scope'),tool=ref('Ref'),connection=ref('Ref'),account_identity=S,destination=S,content=arr(ref('ArtifactRef')),not_before=TIME,expires_at=TIME,preconditions=JSON,configuration_revision=VER,parameters=JSON,cost_bound=ref('Money'))
D['Operation']=obj(id=ID,version=VER,action=ref('Action'),action_digest=DIG,state=enum('prepared','awaiting_review','ready','executing','awaiting_confirmation','outcome_unknown','succeeded','failed','denied','expired','cancelled'),attempt_ids=arr(ID),**{'linked_operation_id?':ID,'relationship?':enum('retry','reconciliation','compensation','replacement')})
D['Review']=obj(id=ID,version=VER,scope=ref('Scope'),action_digest=DIG,preview=ref('Action'),requirement=ref('DecisionRequirement'),proposer_id=ID,state=enum('pending','approved','rejected','expired','invalidated'),**{'decision_id?':ID})
D['Decision']=obj(id=ID,review_id=ID,review_version=VER,action_digest=DIG,reviewer_id=ID,decision=enum('approve','reject'),at=TIME,reason=S)
D['Artifact']=obj(id=ID,version=VER,scope=ref('Scope'),digest=DIG,size=INT,media_type=S,classification=enum('internal','public','restricted'),encrypted=BOOL,state=enum('available','fault'),created_at=TIME)
D['Upload']=obj(id=ID,version=VER,scope=ref('Scope'),expected_size=INT,expected_digest=DIG,received_size=INT,expires_at=TIME,state=enum('open','finished','cancelled','expired'))
D['MemoryBinding']=obj(id=ID,version=VER,scope=ref('Scope'),brain_id=ID,permissions=arr(enum('read','write','curate','promote','retract')),classification=enum('internal','public','restricted'))
D['Claim']=obj(id=ID,version=VER,brain_id=ID,text=S,sources=arr(ref('ArtifactRef')),confidence={'type':'integer','minimum':0,'maximum':1000000},freshness=TIME,active=BOOL,**{'source_brain_id?':ID,'source_claim?':ref('Ref'),'curator_id?':ID,'redaction?':S})
D['Message']=obj(id=ID,version=VER,sender_id=ID,recipient_ids=arr(ID),scope=ref('Scope'),task_ids=arr(ID),body=S,attachments=arr(ref('ArtifactRef')),state=enum('submitted','admitted','acknowledged'),created_at=TIME,**{'conversation_id?':ID})
D['Conversation']=obj(id=ID,version=VER,scope=ref('Scope'),kind=enum('direct','group'),participant_ids=arr(ID),title=S,pinned=BOOL,**{'last_meaningful_event?':TIME})
D['Event']=obj(id=ID,sequence=VER,at=TIME,scope=ref('Scope'),kind=S,resource_id=ID,resource_version=VER,data=JSON)
D['Command']=obj(id=ID,principal_id=ID,operation=S,operation_version=VER,submission_key=S,request_digest=DIG,status=enum('completed','accepted','failed'),data=JSON,**{'error_code?':S})
D['Backup']=obj(id=ID,version=VER,artifact=ref('ArtifactRef'),installation_id=ID,created_at=TIME,brain_revisions=arr(ref('Ref')),key_prerequisites=arr(S),verified=BOOL)
D['Job']=obj(id=ID,version=VER,kind=S,state=enum('pending','running','succeeded','failed','outcome_unknown','cancelled'),requirements=arr(ref('Requirement')),**{'result_artifact?':ref('ArtifactRef'),'operation_id?':ID})
D['Reservation']=obj(id=ID,version=VER,scope=ref('Scope'),root_task_id=ID,operation_id=ID,amount=ref('Money'),state=enum('reserved','settled','unknown','released'))
D['Plan']=obj(id=ID,version=VER,draft_id=ID,base_revision=VER,candidate_digest=DIG,changes=arr(JSON),dependencies=arr(ref('Ref')),compiler_version=S,schema_version=S,authority_requirements=arr(ref('Requirement')),decisions=arr(ref('DecisionRequirement')),diagnostics=arr(ref('Diagnostic')),requirements=arr(ref('Requirement')))
D['Draft']=obj(id=ID,version=VER,base_revision=VER,changes=arr(JSON),diagnostics=arr(ref('Diagnostic')))
D['Revision']=obj(id=ID,version=VER,plan_id=ID,candidate_digest=DIG,activated_at=TIME)
D['Status']=obj(installation_id=ID,generation=VER,paused=BOOL,maintenance=BOOL,initialized=BOOL,requirements=arr(ref('Requirement')))
D['Disposition']=obj(id=ID,version=VER,state=S,**{'job?':ref('Job'),'operation?':ref('Operation'),'requirements?':arr(ref('Requirement'))})
D['Descriptor']=obj(id=S,version=VER,owner=S,input_schema=JSON,output_schema=JSON,effect=S,scope_requirements=arr(S),cli=arr(S),mcp=S,submission_key=BOOL,expected_version=BOOL)

OPS=[]
def add(name,owner,inp,out,behavior,mode='mutation',effect='local',visibility='public',callers=None):
    OPS.append(dict(id=name,version=1,owner=owner,visibility=visibility,mode=mode,effect=effect,
        input_schema=inp,output_schema=out,behavior=behavior,
        cli=(['capabilities'] if name=='capabilities.list' else ['init'] if name=='installation.init' else name.split('.')) if visibility=='public' else [],
        mcp=('zatiti_capabilities' if name=='capabilities.list' else 'zatiti_'+name.replace('.','_')) if visibility=='public' else None,
        submission_key=mode=='mutation' and name!='installation.init' and visibility=='public',
        expected_version='expected_version' in inp.get('required',[]),callers=callers or []))
SC={'scope':ref('Scope')}
KEY={**SC,'id':ID}
EDIT={**KEY,'expected_version':VER}
PAGE={**SC,'cursor?':S,'limit?':{'type':'integer','minimum':1,'maximum':200},'filter?':S}
def one(t): return obj(resource=ref(t))
def page(t): return obj(items=arr(ref(t),500))
def without_generated(t):
    x=deepcopy(D[t]); generated={'id','version','created_at','activated_at','state','validation_state','validated_at','valid_until','diagnostics','next_wake','last_meaningful_event'}
    for k in generated: x['properties'].pop(k,None)
    x['required']=[k for k in x['required'] if k not in generated]
    return x

def resource(family,owner,t,verbs=('create','list','get','update','archive'),draft=True):
    for verb in verbs:
        name=family+'.'+verb
        if verb=='list': add(name,owner,fields(PAGE),page(t),'Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query.',mode='query'); continue
        if verb=='get': add(name,owner,fields(KEY),one(t),'Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.',mode='query'); continue
        if verb=='create': inp=fields({**SC,'definition':without_generated(t),'draft_id?':ID} if draft else {**SC,'definition':without_generated(t)})
        elif verb=='update': inp=fields({**EDIT,'definition':without_generated(t),'draft_id?':ID} if draft else {**EDIT,'definition':without_generated(t)})
        else: inp=fields({**EDIT,'draft_id?':ID} if draft else EDIT)
        text=('Stage a typed '+verb+' in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.' if draft else 'Apply '+verb+' with current authorization, expected version where existing, durable command replay and atomic evidence. Revocation is immediate restriction.')
        add(name,owner,inp,obj(draft=ref('Draft'),resource=ref(t)) if draft else one(t),text)

def export_import(family,owner,t):
    add(family+'.export',owner,fields(KEY),obj(job=ref('Job')),'Create a bounded artifact job containing zatiti.organization/v1 portable definitions scoped to this resource. Exclude secrets, credentials and runtime history. Opaque credential refs require explicit destination rebinding.')
    add(family+'.import',owner,fields({**SC,'artifact':ref('ArtifactRef'),'rebindings':arr(obj(source_ref=S,destination_ref=S)),'draft_id?':ID}),obj(draft=ref('Draft'),diagnostics=arr(ref('Diagnostic'))),'Validate schema, IDs, references, rebinding and canonical digest, then stage a draft. Cross-installation import never transfers live authority.')

# Complete public catalog: definition operations stage configuration; runtime commands are explicit.
for f,t in [('principal','Principal'),('grant','Grant')]:
    resource(f,'identity',t,('create','list','get','update','revoke'),draft=False)
add('credential.provision','identity',fields({**SC,'principal_id':ID,'store_ref':S,'expires_at?':TIME}),one('Credential'),'Attach a helper-provisioned opaque local reference; authenticate its binding. Never accept or return secret bytes.')
add('credential.revoke','identity',fields(EDIT),one('Credential'),'Commit revocation immediately; future authentication and claims fail. Retain revocation through restore.')
for f,t in [('organization','Organization'),('team','Team'),('project','Project'),('worker','Worker'),('binding','Binding'),('execution_profile','ExecutionProfile')]:
    resource(f,'configuration',t)
    if f in ('organization','team','project'): export_import(f,'configuration',t)
# Organization creation takes chief definition together, generated chief ID is server assigned.
o=next(x for x in OPS if x['id']=='organization.create')
o['input_schema']['properties']['definition']['properties'].pop('chief_id',None)
o['input_schema']['properties']['definition']['required'].remove('chief_id')
o['input_schema']['properties']['chief']=without_generated('Worker')
o['input_schema']['properties']['chief']['properties'].pop('organization_id',None)
o['input_schema']['properties']['chief']['required'].remove('organization_id')
o['input_schema']['required'].append('chief')
o['behavior']+=' Allocate organization and designated chief IDs together; bootstrap can create a non-executable minimum-permission chief before provider/limits exist. Ordinary worker creation creates no organization.'
for name,extra in [('organization.move',{'parent_id':ID}),('organization.chief.replace',{'chief_id':ID}),('worker.move',{'organization_id':ID})]:
    add(name,'configuration',fields({**EDIT,**extra,'draft_id?':ID}),obj(draft=ref('Draft')),'Stage explicit atomic change. Reject ancestry cycles, recheck inherited authority/budgets and memory bindings. Preserve organization identity, memory, obligations and history. Do not transfer private memory or widen active-work authority.')
for verb in ('pause','resume'):
    add('worker.'+verb,'execution',fields(EDIT),one('Disposition'),'Pause immediately blocks new worker admissions without inference or spending; resume requires current normal authority. Existing external effects remain visible.')
resource('configuration.draft','configuration','Draft',('list','get'),False)
add('configuration.draft.create','configuration',fields({**SC,'base_revision':VER}),one('Draft'),'Create empty draft against current revision.')
add('configuration.draft.update','configuration',fields({**EDIT,'changes':arr(obj(kind=S,action=enum('create','update','archive','delete'),id=ID,expected_version=INT,definition=JSON))}),one('Draft'),'Validate each change against its concrete kind schema. Complete definitions, no implicit deletion. Unknown kind or fields fail. Changes remain inactive.')
add('configuration.draft.discard','configuration',fields(EDIT),one('Disposition'),'Discard only draft; preserve plan lineage and evidence.')
add('configuration.plan','configuration',fields({**SC,'draft_id':ID,'expected_version':VER}),one('Plan'),'Seal canonical candidate, base revision, changed objects, dependency identities, compiler/schema versions, authority delta and prerequisites. Return exact preview and missing requirements; no live activation.')
resource('configuration.plan','configuration','Plan',('list','get'),False)
add('configuration.apply','configuration',fields({**SC,'plan_id':ID,'base_revision':VER,'candidate_digest':DIG}),one('Revision'),'Atomically recheck current head, old authority, restrictions, dependency/prerequisite validity and exact decisions, activate complete bundle and append event. Stale plans fail, identical key replay returns original result.')
resource('configuration.revision','configuration','Revision',('list','get'),False)
add('configuration.rollback.plan','configuration',fields({**SC,'revision_id':ID,'base_revision':VER}),one('Plan'),'Build a new plan against current head restoring eligible definitions; no database rewind or erased obligations.')
resource('skill','skills','Skill',('list','get','archive'))
add('skill.import','skills',fields({**SC,'artifact':ref('ArtifactRef'),'source':S,'license':S,'draft_id?':ID}),one('Skill'),'Stage immutable content-hashed skill after bounded archive validation. Never execute imported scripts; reject traversal, escaping symlinks, devices, collisions and dependency cycles.')
add('skill.evaluate','skills',fields({**SC,'skill':ref('Ref'),'acceptance':ref('Acceptance'),'profile':ref('ExecutionProfile'),'limits':ref('Limits')}),one('Job'),'Create admitted evaluation with sealed fixtures/evaluator/profile versions. Skill or evaluator changes invalidate only dependent qualifications; evaluation grants no authority.')
add('skill.evaluation.status','skills',fields(KEY),one('Job'),'Inspect actual evaluation disposition and retained evidence.',mode='query')
resource('tool','connections','Tool',('list','get'),False)
add('tool.schema','connections',fields(KEY),obj(input_schema=JSON,output_schema=JSON),'Inspect exact pinned contract; discovery installs no authority.',mode='query')
for v in ('bind','unbind'):
    add('tool.'+v,'connections',fields({**SC,'binding':ref('Binding'),'draft_id?':ID}),one('Draft'),'Stage explicit tool binding change through compiler; reject unknown adapter or unqualified executable binding.')
resource('connection','connections','Connection')
add('connection.validate','connections',fields(EDIT),one('Job'),'Admit a bounded separately authorized provider probe. Record observed account/scopes, timestamp and freshness. Mismatch cannot silently substitute accounts.',effect='external_read')
for v in ('begin','status','complete','cancel'):
    inp=fields({**SC,'connection_id':ID,'expected_version':VER,'method':enum('browser','store_reference')}) if v=='begin' else fields({**SC,'challenge_id':ID,**({'expected_version':VER} if v!='status' else {}),**({'helper_ref':S} if v=='complete' else {})})
    add('connection.setup.'+v,'connections',inp,one('Challenge'),'Typed credential challenge: '+v+'. Trusted local helper handles all codes/tokens outside model-visible data. Begin may return external_action_required with challenge; complete validates bound helper result/account/current authority. Expired/cancelled challenges cannot complete.',mode='query' if v=='status' else 'mutation')
add('connection.rotate','connections',fields({**EDIT,'store_ref':S}),one('Job'),'Same-account rotation validates account identity before replacement. Account substitution requires a new exact configuration/review under old policy.',effect='external_read')
add('connection.revoke','connections',fields(EDIT),one('Disposition'),'Immediately block credential use and future dispatch; retain unresolved effects and secret cleanup obligation.')
resource('policy','policy','Policy')
add('policy.explain','policy',fields({**SC,'action':ref('Action')}),obj(decision=enum('allow','deny','review','prerequisite_missing'),reasons=arr(S),requirements=arr(ref('DecisionRequirement'))),'Evaluate current intersected authority and explain exact action without granting or dispatching it.',mode='query')
resource('autonomy.rule','policy','PromotionRule')
resource('autonomy.qualification','policy','Qualification',('list','get'),False)
add('autonomy.propose','policy',fields({**SC,'worker_id':ID,'rule':ref('Ref'),'evidence_ids':arr(ID)}),one('Qualification'),'Record scoped proposal; proposer cannot approve own evidence or widen criteria/ceiling.')
add('autonomy.evaluate','policy',fields(EDIT),one('Qualification'),'Deterministically evaluate independently established evidence against exact prior rule/version and configuration. Apply narrowly eligible grant only within old ceiling; mandatory human classes remain. Otherwise create review/denial.')
for v in ('restrict','demote'):
    add('autonomy.'+v,'policy',fields({**EDIT,'reason':S,'evidence_ids':arr(ID)}),one('Qualification'),'Commit immediate capability-specific restriction before any further admission, append explanatory event, retain evidence and grant history.')
resource('task','tasks','Task',('list','get'),False)
add('task.create','tasks',fields({**SC,'definition':without_generated('Task')}),one('Task'),'Create bounded durable task with pinned outcome/inputs/acceptance/limits. Validate references, finite envelopes and worker home/bindings; ready only if prerequisites hold.')
add('task.update','tasks',fields({**EDIT,'inputs':arr(ref('ArtifactRef')),'outcome?':S}),one('Task'),'Update only pending draft/ready input under version check; accepted verifier cannot be changed during execution.')
add('task.assign','tasks',fields({**EDIT,'worker_id':ID}),one('Task'),'Versioned assignment shared by direct user and chief requests; conflicting concurrent assignment fails.')
add('task.delegate','tasks',fields({**EDIT,'child':without_generated('Task')}),one('Task'),'Create linked child with intersected permissions/data/deadline, shared root budgets and finite depth/count/concurrency. Expansion is denied.')
for v in ('cancel','retry'):
    add('task.'+v,'tasks',fields({**EDIT,'reason':S}),one('Task'),'Cancel first records intent and blocks new work; terminal only after owned execution stops or is conclusively fenced, retaining unresolved effects. Retry creates new run attempt under unchanged acceptance and requires safe replacement disposition.')
add('task.dependencies','tasks',fields(KEY),obj(dependencies=arr(ref('Task'))),'Inspect dependency states without treating worker claims or failed verification as success.',mode='query')
add('task.accept','tasks',fields({**EDIT,'decision':enum('accept','reject'),'evidence_ids':arr(ID),'reason':S}),one('Task'),'Eligible manual acceptance only for a manual contract; label manual distinctly. Worker cannot self-accept by report. Independent contracts need verifier observations.')
resource('schedule','scheduling','Schedule')
resource('responsibility','scheduling','Responsibility')
for f in ('schedule','responsibility'):
    for v in ('pause','resume'):
        add(f+'.'+v,'scheduling',fields(EDIT),one('Disposition'),'Pause disables future admissions immediately; resume requires current authority. Responsibility pause does not cancel unrelated tasks. Preserve durable next-wake/occurrence identity.')
for f,t in [('run','Run'),('attempt','Attempt')]:
    resource(f,'execution',t,('list','get'),False)
    add(f+'.cancel','execution',fields({**EDIT,'reason':S}),one('Disposition'),'Commit cancellation intent and fence governed work as applicable; do not assert external process stopped. Preserve uncertain effects.')
    add(f+'.recovery','execution',fields(KEY),obj(resource=ref(t),obligations=arr(ref('Requirement'))),'Inspect generation, leases, conflicting resources and effect obligations before replacement.',mode='query')
add('run.claim','execution',fields({**SC,'run_id':ID,'worker_id':ID,'expected_version':VER,'capabilities':arr(S)}),obj(attempt=ref('Attempt'),task=ref('Task'),context=ref('ArtifactRef')),'Atomically claim exactly one current attempt and reserve envelope; bind scoped caller, worker, lease and generation. Lost acknowledgement replays original claim. Reject unsupported required executor guarantees.')
LEASE={**SC,'attempt_id':ID,'lease_id':ID,'generation':VER,'expected_version':VER}
add('attempt.heartbeat','execution',fields(LEASE),one('Attempt'),'Extend current valid lease only for bound identity/generation; stale heartbeats cannot revive fenced attempts.')
add('attempt.checkpoint','execution',fields({**LEASE,'context':ref('ArtifactRef'),'outputs':arr(ref('ArtifactRef'))}),one('Attempt'),'Persist reconstructable context and output disposition at safe boundary with pinned versions; declare external capture limits.')
add('attempt.report','execution',fields({**LEASE,'outputs':arr(ref('ArtifactRef')),'observations':JSON,'usage':ref('Usage')}),one('Attempt'),'Persist observations and enter independent verification; process exit/report never directly succeeds task. Reject stale/wrong-attempt reports.')
add('run.export','execution',fields(KEY),one('Job'),'Export authorized bounded run history artifact separately from portable definitions and encrypted backups.')
resource('review','reviews','Review',('list','get'),False)
add('review.decide','reviews',fields({**EDIT,'action_digest':DIG,'decision':enum('approve','reject'),'reason':S}),one('Decision'),'Check current principal kind/eligibility, separation, expiry, version and exact digest; immutable decision. Agent claims of human approval are invalid input, not authority.')
add('review.delegate','reviews',fields({**EDIT,'principal_id':ID}),one('Review'),'Delegate only inside existing reviewer authority and eligible kind; human-required and proposer separation remain enforced.')
resource('operation','effects','Operation',('list','get'),False)
add('operation.propose','effects',fields({**SC,'action':ref('Action')}),one('Operation'),'Canonicalize immutable exact action, resolve current prerequisites/policy and create logical operation. No provider call in handler.')
add('operation.reconcile','effects',fields(EDIT),one('Job'),'Create separately admitted bounded reconciliation read; retain original unknown outcome and reservation until supported evidence resolves it.',effect='external_read')
for v in ('compensation','replacement'):
    add('operation.'+v+'.propose','effects',fields({**EDIT,'action':ref('Action')}),one('Operation'),'New linked '+v+' operation with own review/budget and consequences; original history remains unchanged.')
resource('artifact','artifacts','Artifact',('list','get'),False)
add('artifact.upload.begin','artifacts',fields({**SC,'size':INT,'digest':DIG,'media_type':S,'classification':enum('internal','public','restricted')}),one('Upload'),'Allocate bounded staged upload tied to caller/scope/digest/size; no published artifact yet.')
add('artifact.upload.chunk','artifacts',fields({**SC,'upload_id':ID,'offset':INT,'bytes_base64':{'type':'string','maxLength':1398104},'chunk_digest':DIG}),one('Upload'),'Write staged bounded chunk outside DB transaction via prepare/record phases. Same offset and bytes replay; different bytes conflict. No arbitrary server path.')
add('artifact.upload.finish','artifacts',fields({**SC,'upload_id':ID,'expected_version':VER}),one('Artifact'),'Hash/size verify complete staged bytes, publish content-addressed file, then commit metadata/event. Missing committed bytes produce artifact_fault; orphan bytes are reclaimed later.')
add('artifact.upload.cancel','artifacts',fields({**SC,'upload_id':ID,'expected_version':VER}),one('Disposition'),'Cancel unpublished upload and queue safe staged-content cleanup; do not delete referenced content.')
add('artifact.read','artifacts',fields({**KEY,'offset':INT,'length':{'type':'integer','minimum':1,'maximum':1048576}}),obj(bytes_base64=S,digest=DIG,offset=INT,total_size=INT),'Read authorized bounded range outside transaction after metadata access; integrity/auth checks precede disclosure. Max encoded output follows 1 MiB decoded.',mode='query')
add('artifact.export','artifacts',fields(KEY),one('Job'),'Produce authorized export artifact; client chooses local output path, no server path input.')
add('event.list','evidence',fields({**PAGE,'limit?':{'type':'integer','minimum':1,'maximum':500}}),page('Event'),'Replay authorized events after opaque cursor with explicit gap/expiry handling; fetch snapshot when expired.',mode='query')
add('event.get','evidence',fields(KEY),one('Event'),'Read authorized immutable event.',mode='query')
add('command.get','evidence',fields({**SC,'submission_key':S,'operation':S,'operation_version':VER}),one('Command'),'Look up caller-bound durable disposition after lost acknowledgement; never return another principal command.',mode='query')
add('budget.get','accounting',fields(SC),obj(limits=ref('Limits')),'Inspect effective intersected installation/ancestor/project/worker/root limits.',mode='query')
add('budget.propose','accounting',fields({**SC,'expected_version':VER,'limits':ref('Limits'),'draft_id?':ID}),one('Draft'),'Stage exact budget change under old authority; no paid execution until explicit currency/finite spend.')
add('usage.get','accounting',fields(SC),one('Usage'),'Return spent/reserved/estimated/unknown separately; advisory/missing price is not zero.',mode='query')
resource('memory.binding','memory','MemoryBinding')
add('memory.recall','memory',fields({**SC,'query':S,'binding_ids':arr(ID),'minimum_freshness':TIME,'limits':ref('Limits')}),one('Job'),'Authorize and select brains BEFORE retrieval/model composition. Recall may charge/disclose and uses governed effects. Store actual context artifact with scope/sources/confidence/versions/freshness; unavailable bound => wait/refusal.',effect='disclosure')
add('memory.remember','memory',fields({**SC,'binding_id':ID,'text':S,'sources':arr(ref('ArtifactRef')),'limits':ref('Limits')}),one('Job'),'Persist canonical writer intent/command ID before dispatch to exactly one Serenity writer for brain; lost acknowledgement requires reconciliation, not blind retry.',effect='external_mutation')
add('memory.inspect','memory',fields({**SC,'brain_id':ID,'claim_id':ID}),one('Claim'),'Inspect authorized scoped claim through public adapter facade; no unbound brain access. Potential provider work still requires separately admitted job.',mode='query')
add('memory.promote','memory',fields({**SC,'source_brain_id':ID,'source_claim':ref('Ref'),'destination_binding_id':ID,'redaction?':S,'limits':ref('Limits')}),one('Job'),'Require source disclosure and destination write/curate authority. New destination claim retains source/version/evidence/curator/redaction lineage; copies are not independent corroboration.',effect='external_mutation')
add('memory.retract','memory',fields({**SC,'brain_id':ID,'claim':ref('Ref'),'reason':S}),one('Job'),'Immediately exclude revoked claim from future recall, persist downstream promotion reconciliation obligations, explain active-recall removal versus historical erasure.',effect='external_mutation')
add('memory.job.get','memory',fields(KEY),one('Job'),'Inspect actual memory disposition, prerequisites, context artifact and unresolved writer obligations.',mode='query')
resource('conversation','messaging','Conversation',('list','get'),False)
add('conversation.create','messaging',fields({**SC,'kind':enum('direct','group'),'participant_ids':arr(ID),'title':S}),one('Conversation'),'Create conversation without changing home organization, memory access or tool grants; validate participant disclosure bindings.')
add('conversation.update','messaging',fields({**EDIT,'participant_ids?':arr(ID),'title?':S,'pinned?':BOOL}),one('Conversation'),'Versioned membership/title updates require governed disclosure; joining grants no historical restricted material automatically.')
add('conversation.message.send','messaging',fields({**SC,'conversation_id':ID,'message_id':ID,'body':S,'attachments':arr(ref('ArtifactRef')),'task_ids':arr(ID)}),one('Message'),'Durably admit authenticated user message; attachments/participants are governed disclosures. Task creation/assignment uses existing versioned task operations, not duplicate scheduling.',effect='disclosure')
add('mailbox.send','messaging',fields({**SC,'message_id':ID,'recipient_id':ID,'body':S,'attachments':arr(ref('ArtifactRef')),'task_ids':arr(ID)}),one('Message'),'Record authenticated sender, scope and stable identity; acknowledge receipt only after durable target admission. Redelivery deduplicates identity and checks changed content conflict.',effect='disclosure')
add('mailbox.list','messaging',fields({**PAGE,'recipient_id':ID}),page('Message'),'Read authorized admitted messages; no authority from body text.',mode='query')
add('mailbox.ack','messaging',fields({**SC,'message_id':ID,'recipient_id':ID,'expected_version':VER}),one('Message'),'Acknowledge durable target admission at safe worker boundary; enforce recipient/current attempt where worker-bound.')
add('capabilities.list','registry',fields({'scope?':ref('Scope')}),page('Descriptor'),'Enumerate complete public operation catalog/support versions without granting authority.',mode='query')
add('capabilities.schema','registry',obj(operation=S,version=VER),one('Descriptor'),'Return exact schemas, names, semantics and availability prerequisites; no hidden product operations.',mode='query')
add('installation.init','installation',obj(credential_store=enum('os','headless'),owner_name=S,**{'headless_key_ref?':S}),one('Status'),'One-time OS-authorized local bootstrap acquires exclusive lock and creates installation, owner credential and personal organization/chief together. Return metadata only. Refuse initialized destination. End bootstrap session; paid work remains unavailable.')
for v in ('status','doctor'):
    add('installation.'+v,'installation',fields(SC),one('Status'),'Inspect acknowledged owner/generation/paused/maintenance state and named prerequisites/limitations with secret-free diagnostics.',mode='query')
for v in ('pause','resume','maintenance.enter'):
    add('installation.'+v,'installation',fields({**SC,'expected_version':VER}),one('Status'),'Pause/maintenance enter immediately blocks new admissions; maintenance drains/fences work and records unresolved effects. Resume rechecks current authority and recovery prerequisites; never claims in-flight bytes were retracted.')
add('installation.backup','installation',fields(SC),one('Job'),'Create encrypted verified consistent SQLite+artifact+brain manifest backup. Return opaque backup artifact; preserve separate key-recovery prerequisites. Never copy a live SQLite file casually.')
add('installation.restore','installation',fields({**SC,'backup_artifact':ref('ArtifactRef'),'expected_version':VER}),one('Job'),'Require exclusive quiesced local-owner maintenance session. Verify encrypted backup/installation bindings/keys. Restore paused, preserve revocation/command identities/reservations and reconcile provider/memory intents before resume.')
add('installation.job.get','installation',fields(KEY),one('Job'),'Inspect durable backup/restore progress and verified results.',mode='query')

# Internal owner methods: callable only by named application/domain owners, never transports.
D['Change']=obj(kind=enum('organization','team','project','worker','binding','execution_profile','skill','connection','policy','autonomy_rule','schedule','responsibility','memory_binding','budget'),action=enum('create','update','archive','delete'),id=ID,expected_version=INT,definition=JSON)
D['Candidate']=obj(plan_id=ID,base_revision=VER,candidate_digest=DIG,changes=arr(ref('Change')),dependencies=arr(ref('Ref')))
D['Validation']=obj(diagnostics=arr(ref('Diagnostic')),requirements=arr(ref('Requirement')),dependencies=arr(ref('Ref')))
D['Authority']=obj(principal=ref('Principal'),grants=arr(ref('Grant')),restrictions=arr(S))
D['ScopeSnapshot']=obj(scope=ref('Scope'),revision=VER,ancestors=arr(ref('Organization')),bindings=arr(ref('Binding')),**{'worker?':ref('Worker'),'project?':ref('Project')})
D['PolicyResult']=obj(decision=enum('allow','deny','review','prerequisite_missing'),reasons=arr(S),requirements=arr(ref('DecisionRequirement')))
D['Dispatch']=obj(operation_id=ID,attempt_id=ID,generation=VER,adapter=S,action=ref('Action'),credential_ref=S,deadline=TIME,**{'provider_key?':S})
D['Observation']=obj(disposition=enum('succeeded','failed','accepted','unknown','not_sent'),evidence=JSON,usage=ref('Usage'),**{'provider_reference?':S,'confirmed_at?':TIME})
D['Wake']=obj(id=ID,scope=ref('Scope'),source_id=ID,occurrence_key=S,due_at=TIME,condition_version=VER)
D['Context']=obj(attempt_id=ID,artifact=ref('ArtifactRef'),configuration_revision=VER,source_artifacts=arr(ref('ArtifactRef')),capture=enum('complete','partial','advisory'))

def internal(name,owner,inp,out,text,callers,mode='mutation'):
    add('_'+owner+'.'+name,owner,inp,out,text,mode=mode,visibility='internal',callers=callers)
for owner in ['identity','configuration','skills','connections','policy','accounting','scheduling','memory']:
    internal('validate',owner,obj(candidate=ref('Candidate')),one('Validation'),'Validate only owned candidate slice against current snapshot, collect dependency identities/requirements; no live changes or network. Candidate changes must match their registered concrete definition schemas. Expected-version zero is create-only. Authorization comes from old effective state.','configuration application'.split(),mode='query')
    internal('activate',owner,obj(candidate=ref('Candidate')),obj(versions=arr(ref('Ref'))),'Apply owned exact sealed candidate slice inside compiler transaction; caller must hold configuration-apply context established by application. No public activation flag or second compiler.','configuration application'.split())
internal('authority','identity',fields({'principal_id':ID,'scope':ref('Scope')}),one('Authority'),'Read current principal, grants, expiry/revocations and restrictions; no grants from claimed profile/name or proposed policy.',['application','policy','reviews','configuration','execution','effects'],mode='query')
internal('bootstrap','identity',obj(owner_id=ID,credential_id=ID,store_ref=S,name=S,installation_id=ID),one('Principal'),'Create one initial human owner and scoped service identities in exclusive bootstrap transaction. Credential bytes were stored by trusted helper beforehand.',['installation'])
internal('restrict','identity',obj(principal_id=ID,capability=S,reason=S),one('Disposition'),'Atomically narrow/revoke effective grant before future admission, never expand.',['policy','installation'])
internal('promote','identity',obj(principal_id=ID,qualification=ref('Qualification'),ceiling_grant_id=ID),one('Grant'),'Activate exact evidence-qualified narrow grant after old-policy rule check; verify ceiling/current rule version and immutable qualification.',['policy'])
internal('stage','configuration',obj(scope=ref('Scope'),change=ref('Change'),**{'draft_id?':ID}),one('Draft'),'Strictly validate typed definition schema then append draft change. No effective mutation. For creates allocate identity once using submission replay.',['configuration','skills','connections','policy','accounting','scheduling','memory'])
internal('snapshot','configuration',fields(SC),one('ScopeSnapshot'),'Read current ancestry, effective bindings, worker/project and revision; no automatic descendant private data access.',['application','policy','tasks','execution','effects','memory','reviews','accounting','scheduling','messaging','connections','installation'],mode='query')
internal('bootstrap','configuration',obj(installation_id=ID,owner_id=ID,organization_id=ID,chief_id=ID),obj(organization=ref('Organization'),chief=ref('Worker')),'Atomically initialize root organization/chief with minimal non-executable configuration. Separate worker/org/installation brains provisioned through memory owner.',['installation'])
internal('check','policy',obj(scope=ref('Scope'),capability=S,**{'action?':ref('Action'),'candidate_digest?':DIG}),one('PolicyResult'),'Intersect authenticated current grants, ancestry/bindings, task/worker scope, policy, restrictions and required conditions. Explicit deny wins; unknown required conditions fail closed. Check exact human review requirements without accepting user assertions.',['application','configuration','tasks','execution','effects','memory','messaging','connections','installation','reviews','accounting'],mode='query')
internal('invalidate','policy',obj(changed_dependencies=arr(ref('Ref')),reason=S),obj(qualification_ids=arr(ID)),'Invalidate only dependent qualifications, immediately restrict affected grants and emit explanations before further admission.',['configuration','skills','connections','execution','tasks'])
internal('ensure','reviews',obj(scope=ref('Scope'),action=ref('Action'),requirement=ref('DecisionRequirement')),one('Review'),'Create or inspect exact digest-bound review, retaining immutable history and eligibility constraints.',['effects','configuration','policy'])
internal('check','reviews',obj(scope=ref('Scope'),action_digest=DIG),obj(eligible=BOOL,**{'decision?':ref('Decision')}),'Recheck current eligible reviewer/grants, principal kind, expiry, version, proposer separation and exact action digest; false is not permission.',['effects','configuration','policy','tasks'],mode='query')
internal('reserve','accounting',obj(scope=ref('Scope'),root_task_id=ID,operation_id=ID,amount=ref('Money'),limits=ref('Limits')),one('Reservation'),'Reserve enforceable cost and concurrency in stable installation→ancestor organizations→project→worker→root order, inside caller transaction. All dimensions atomic; reject unknown price/advisory hard-cap claim.',['effects','execution','tasks','scheduling'])
internal('settle','accounting',obj(reservation_id=ID,expected_version=VER,usage=ref('Usage'),authoritative_nonexecution=BOOL),one('Reservation'),'Settle observed cost or retain unknown reservation; release only proven unused portion and conclusive no-effect/no-cost evidence. Check currency and overflow.',['effects','execution','installation'])
internal('inspect','accounting',fields(SC),obj(limits=ref('Limits'),usage=ref('Usage')),'Return current intersected limits and honest usage to admission/doctor.',['policy','tasks','execution','effects','scheduling','installation'],mode='query')
internal('resolve','connections',obj(scope=ref('Scope'),connection=ref('Ref'),tool=ref('Ref'),destination=S),obj(connection=ref('Connection'),tool=ref('Tool')),'Resolve exact validated account/tool/destination/binding and current revocation/freshness; return opaque credential reference only to trusted dispatcher.',['effects','execution','memory','skills','configuration'],mode='query')
internal('validation.record','connections',obj(connection_id=ID,expected_version=VER,observation=ref('Observation')),one('Connection'),'Record authorized probe identity/scopes/freshness and invalidation without accepting account substitution.',['effects','controller'])
internal('create','tasks',obj(task=ref('Task'),source_id=ID,occurrence_key=S),one('Task'),'Create/deduplicate admitted task from wake/responsibility/conversation by source+occurrence identity; differing content conflicts. Enforce current bounds and bindings.',['scheduling','messaging','execution'])
internal('snapshot','tasks',fields(KEY),one('Task'),'Return current pinned contract and task scope, parent/root/dependency state; caller still obeys authority.',['execution','effects','scheduling','memory','reviews','policy'],mode='query')
internal('transition','tasks',obj(task_id=ID,expected_version=VER,state=D['Task']['properties']['state'],evidence_ids=arr(ID),**{'waiting_reason?':S,'manual?':BOOL} ),one('Task'),'Validate legal transition and pinned independent acceptance. succeeded requires verifier-established observations or eligible explicitly manual acceptance; failed verification never releases success dependents.',['execution','scheduling','installation'])
internal('wake.due','scheduling',obj(now=TIME,limit={'type':'integer','minimum':1,'maximum':100}),obj(wakes=arr(ref('Wake'),100)),'Read due wakes under current generation; reading does not admit occurrence.',['controller'],mode='query')
internal('wake.admit','scheduling',obj(wake=ref('Wake')),obj(**{'task?':ref('Task'),'skipped':BOOL}),'Recheck pause/cancel/reply conditions transactionally, deduplicate occurrence key, create task/cycle and persist next wake in same transaction.',['controller'])
internal('cycle.record','scheduling',obj(responsibility_id=ID,expected_version=VER,next_wake=TIME,outputs=arr(ref('ArtifactRef')),task_ids=arr(ID)),one('Responsibility'),'Persist bounded cycle outcome/next-wake, enforce minimum interval and per-cycle plus aggregate limits.',['execution'])
internal('admit','effects',obj(operation_id=ID,expected_version=VER),one('Operation'),'Atomic current policy/review/preconditions, complete budget reservation, physical attempt/dispatch intent and outbox event. Operation action is immutable.',['controller','execution','memory','skills','connections'])
internal('claim','effects',obj(operation_id=ID,attempt_id=ID,generation=VER),one('Dispatch'),'Consume one-use attempt claim after current generation, revocation, expiry, restriction and action checks. Dispatch never returned to cooperative workers.',['controller'])
internal('record','effects',obj(operation_id=ID,attempt_id=ID,generation=VER,observation=ref('Observation')),one('Operation'),'Store actual provider observation plus cost settlement/uncertainty atomically. Lost record is recoverable without blind resend. Contradictory late evidence records correction/dispute.',['controller'])
internal('pending','effects',obj(limit={'type':'integer','minimum':1,'maximum':100}),obj(operations=arr(ref('Operation'),100)),'List pending intents/confirmation/reconciliation obligations; claimed attempts after restart become unknown, not ready for resend.',['controller','installation'],mode='query')
internal('prepare','effects',obj(scope=ref('Scope'),action=ref('Action'),source_id=ID),one('Operation'),'Persist immutable action and logical effect for hosted steps/memory/probes/evaluation; required decisions produce awaiting_review. No physical call here.',['execution','memory','connections','skills','installation'])
internal('tick','execution',obj(now=TIME,limit={'type':'integer','minimum':1,'maximum':100}),obj(attempt_ids=arr(ID)),'Admit bounded owned work/expire leases; persist request context artifact before preparing model effect. Any blob staging happens via IO boundary before this method. No network inside transaction.',['controller'])
internal('context','execution',obj(attempt_id=ID,context=ref('Context')),one('Attempt'),'Pin persisted model-visible messages/instructions/tools/results/memory/compaction lineage and safe-boundary mailbox injection before dispatch.',['controller'])
internal('observation','execution',obj(attempt_id=ID,operation_id=ID,observation=ref('Observation')),one('Attempt'),'Continue bounded model loop from recorded effect; dispatch declared tools via effects owner, persist output context, verify submitted task independently.',['controller'])
internal('fence','execution',obj(generation=VER,reason=S),obj(attempt_ids=arr(ID)),'Fence old governed leases and record recovery obligations; never assert external processes stopped.',['controller','installation'])
internal('admit','messaging',obj(message=ref('Message')),one('Message'),'Deduplicate message identity; commit recipient inbox before receipt; preserve scope/attachments and treat content as untrusted.',['execution','controller','scheduling'])
internal('pending','messaging',obj(worker_id=ID,limit={'type':'integer','minimum':1,'maximum':100}),page('Message'),'Get authorized inbox messages for safe-boundary injection or idle resume, without trusting messages as grants.',['execution','scheduling'],mode='query')
internal('bootstrap','messaging',obj(scope=ref('Scope'),owner_id=ID,chief_id=ID),one('Conversation'),'Create pinned personal-chief direct conversation from committed identities.',['installation'])
internal('select','memory',obj(scope=ref('Scope'),binding_ids=arr(ID),permission=enum('read','write','curate','promote','retract'),minimum_freshness=TIME),obj(bindings=arr(ref('MemoryBinding'))),'Filter current authorized brains before retrieval; parentage/group membership grants no access. Unavailable freshness creates prerequisite failure.',['execution','effects','configuration'],mode='query')
internal('record','memory',obj(job_id=ID,operation_id=ID,observation=ref('Observation')),one('Job'),'Record actual writer/recall outcome and provenance/context artifact; lost acknowledgement retains obligation; corrections follow durable promotion lineage.',['controller'])
internal('bootstrap','memory',obj(installation_id=ID,organization_id=ID,chief_id=ID),obj(bindings=arr(ref('MemoryBinding'))),'Allocate distinct installation/organization/chief brains and explicit minimal bindings; durable writer provision jobs do not claim external service is already available.',['installation'])
internal('manifest','memory',fields(SC),obj(brain_revisions=arr(ref('Ref')),obligations=arr(ref('Requirement'))),'Return qualified brain revisions and unresolved writer/promotion obligations for paused backup/restore.',['installation'],mode='query')
internal('metadata','artifacts',obj(scope=ref('Scope'),artifacts=arr(ref('ArtifactRef'))),obj(artifacts=arr(ref('Artifact'))),'Validate scope, digests, availability and classification of pinned artifacts before disclosure or acceptance.',['execution','tasks','effects','memory','messaging','skills','installation'],mode='query')
internal('publish','artifacts',obj(scope=ref('Scope'),digest=DIG,size=INT,media_type=S,classification=enum('internal','public','restricted'),encrypted=BOOL),one('Artifact'),'Publish metadata only after trusted IO phase has staged/hashed/published bytes; create visible fault if later bytes unavailable.',['controller','execution','memory','skills','installation'])
internal('command.begin','evidence',obj(principal_id=ID,operation=S,operation_version=VER,submission_key=S,request_digest=DIG),obj(command_id=ID,**{'existing?':ref('Command')}),'Atomically acquire command identity or return original disposition; changed hash conflicts. Retention >=30 days plus obligations.',['application'])
internal('command.finish','evidence',obj(command_id=ID,status=enum('completed','accepted','failed'),data=JSON,**{'error_code?':S}),one('Command'),'Persist handler disposition in same transaction as its state/events; remember denial separately after rollback when required.',['application'])
internal('snapshot','evidence',fields(SC),obj(last_sequence=INT),'Get current event checkpoint with consistent scoped read; no private events through cursor position.',['application','installation','messaging'],mode='query')
internal('restore.record','installation',obj(job_id=ID,state=enum('pending','running','succeeded','failed','outcome_unknown'),requirements=arr(ref('Requirement'))),one('Job'),'Record maintenance job disposition after IO/verification; restored installation remains paused with unresolved obligations intact.',['controller'])

# Tighten common schemas after catalog construction; all $refs resolve transitively.
D['Worker']['properties']['profile']={'anyOf':[ref('ExecutionProfile'),{'type':'null'}]}
D['Worker']['properties']['limits']={'anyOf':[ref('Limits'),{'type':'null'}]}
# Null profile/limits is bootstrap/draft only: activation of executable binding refuses it.
for o in OPS:
    if o['id'] in ('organization.create','worker.create','worker.update'):
        targets=[o['input_schema']['properties'].get('chief'),o['input_schema']['properties'].get('definition')]
        for x in targets:
            if x and 'profile' in x['properties']:
                x['properties']['profile']={'anyOf':[ref('ExecutionProfile'),{'type':'null'}]}
                x['properties']['limits']={'anyOf':[ref('Limits'),{'type':'null'}]}
    if o['id']=='artifact.read': o['output_schema']['properties']['bytes_base64']={'type':'string','maxLength':1398104}
    if o['id']=='configuration.draft.update': o['input_schema']['properties']['changes']=arr(ref('Change'))

DEFINITION_TYPES={'organization':'Organization','team':'Team','project':'Project','worker':'Worker','binding':'Binding','execution_profile':'ExecutionProfile','skill':'Skill','connection':'Connection','policy':'Policy','autonomy_rule':'PromotionRule','schedule':'Schedule','responsibility':'Responsibility','memory_binding':'MemoryBinding','budget':'Limits'}
D['Change']['properties']['definition']={'oneOf':[dict(ref(t),description=k) for k,t in DEFINITION_TYPES.items()]}
# The discriminator kind selects the single matching definition schema; metadata ID/version
# must match Change.id/expected_version. Delete/archive carry the previous full definition.

# Integration-audit corrections: versioned installation writes, task admission and durable jobs.
D['Status']['properties']['version']=VER; D['Status']['required'].append('version')
D['Job']['properties'].update(owner=S,operation=S,**{'result':JSON})
D['Job']['required']+=['owner','operation']
D['Reservation']['required'].remove('root_task_id')
for o in OPS:
    if o['id']=='_accounting.reserve': o['input_schema']['required'].remove('root_task_id')
internal('enqueue','execution',obj(task=ref('Task')),one('Run'),'Create/deduplicate run for ready task/version with pinned configuration/inputs. Called in task creation/admission transaction; does not claim/dispatch yet.',['tasks','scheduling','application'])
internal('ready','tasks',obj(limit={'type':'integer','minimum':1,'maximum':100}),page('Task'),'Bounded recovery scan of ready tasks without current run; execution enqueue deduplicates task/version.',['execution','controller'],mode='query')
add('job.get','execution',fields(KEY),one('Job'),'Inspect authorized durable job state/result/evidence for any owner. Status never equates provider acceptance to confirmed success.',mode='query')
internal('job.create','execution',obj(scope=ref('Scope'),owner=S,operation=S,input=JSON,source_id=ID),one('Job'),'Validate owner/operation against catalog and original input schema; deduplicate source identity with canonical input hash. No arbitrary executable jobs.',['configuration','skills','connections','memory','artifacts','installation','execution','effects','application'])
internal('job.pending','execution',obj(limit={'type':'integer','minimum':1,'maximum':100}),page('Job'),'Read pending or recoverable local jobs with original owner/input and obligations; no second scheduler.',['controller'],mode='query')
internal('job.claim','execution',obj(job_id=ID,expected_version=VER,generation=VER),obj(job=ref('Job'),input=JSON),'Atomically claim one current job owner under generation; abandoned claimed provider intent never blindly redispatched.',['controller','application'])
internal('job.record','execution',obj(job_id=ID,expected_version=VER,generation=VER,state=D['Job']['properties']['state'],result=JSON,evidence_ids=arr(ID)),one('Job'),'Record real result conforming originating operation schema, preserve unknown obligations and actual evidence; result changes require current claim.',['controller','application','effects','memory','skills','connections','installation'])
internal('verification.record','execution',obj(attempt_id=ID,expected_version=VER,verifier_id=S,verifier_version=S,accepted=BOOL,observations=JSON,evidence_ids=arr(ID)),one('Attempt'),'Only trusted controller verifier path; recheck exact accepted verifier/sealed inputs/current task and attempt before independently transitioning task. Worker report cannot call this method.',['controller'])
# Duplicate kind definitions do not overlap because Change.kind and definition are validated together.
# Use discriminated oneOf at Change level, not an uncorrelated union in definition.
D['Change']={'oneOf':[obj(kind={'const':k},action=enum('create','update','archive','delete'),id=ID,expected_version=INT,definition=ref(t)) for k,t in DEFINITION_TYPES.items()]}

# Bring exact local verifier payloads into operation reference closure under a separate namespace.
import json as _json
from pathlib import Path as _Path
_LOCAL=_json.loads((_Path(__file__).resolve().parents[2]/'docs/implementation/adapter-schemas.json').read_text())['definitions']
def _prefix_refs(v):
    if isinstance(v,dict):
        return {k:('#/$defs/Adapter_'+x[8:] if k=='$ref' and isinstance(x,str) and x.startswith('#/$defs/') else _prefix_refs(x)) for k,x in v.items()}
    if isinstance(v,list):return [_prefix_refs(x) for x in v]
    return v
D.update({'Adapter_'+n:_prefix_refs(v) for n,v in _LOCAL.items()})
D['Acceptance']['properties']['expected_observations']=arr(ref('Adapter_ExpectedVerificationObservation'),512)
D['Acceptance']['properties']['profile']=ref('Adapter_VerificationProfile')
D['Acceptance']['required'].append('profile')
D['Usage']['properties']['currency']={'type':'string','pattern':'^[A-Z]{3}$'}
for o in OPS:
    if o['id']=='_execution.verification.record':
        o['input_schema']=obj(attempt_id=ID,expected_version=VER,result=ref('Adapter_VerificationResult'))
    if o['id']=='task.assign':o['callers']=['messaging']
    if 'filter' in o['input_schema'].get('properties',{}):
        o['input_schema']['properties']['filter']=obj(**{'state?':S,'key?':S,'parent_id?':ID,'worker_id?':ID,'task_id?':ID,'organization_id?':ID,'descendants?':BOOL,'needs_you?':BOOL})
        o['behavior']+=' Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.'

# Async completion schemas are distinct from the immediate accepted Job envelope.
_COMPLETIONS={
 'skill.evaluate':obj(evaluation_id=ID,passed=BOOL,evidence=arr(ref('ArtifactRef'))),
 'connection.validate':one('Connection'),'connection.rotate':one('Connection'),
 'operation.reconcile':one('Operation'),
 'memory.recall':obj(context_artifact=ref('ArtifactRef'),claims=arr(ref('Claim')),brain_versions=arr(ref('Ref')),freshness=TIME,requirements=arr(ref('Requirement'))),
 'memory.remember':obj(claims=arr(ref('Claim')),obligations=arr(ref('Requirement'))),
 'memory.promote':obj(claims=arr(ref('Claim')),obligations=arr(ref('Requirement'))),
 'memory.retract':obj(claims=arr(ref('Claim')),obligations=arr(ref('Requirement')),historical_erasure={'const':False}),
 'installation.backup':one('Backup'),'installation.restore':one('Status'),
}
for _o in OPS:
    if _o['id'].endswith('.export'):_COMPLETIONS[_o['id']]=one('Artifact')
_SYNC_IO={'artifact.upload.chunk','artifact.upload.finish','artifact.upload.cancel','skill.import','connection.setup.begin','connection.setup.complete','connection.setup.cancel'}
for _o in OPS:
    if _o['id'] in _COMPLETIONS:_o['completion_schema']=_COMPLETIONS[_o['id']]
    elif _o['id'] in _SYNC_IO:_o['completion_schema']=deepcopy(_o['output_schema'])
    if _o['id'] in _SYNC_IO:
        _o['output_schema']={'oneOf':[deepcopy(_o['output_schema']),obj(job=ref('Job'))]}

D['Fault']=obj(code=S,message=S,retryable=BOOL,**{'details?':JSON})
D['Result']=obj(schema={'const':'zatiti.result/v1'},command_id=ID,status=enum('completed','accepted','failed'),data={'anyOf':[JSON,{'type':'null'}]},error={'anyOf':[ref('Fault'),{'type':'null'}]},next_cursor={'anyOf':[S,{'type':'null'}]})
D['Command']['properties']['result']=ref('Result');D['Command']['required'].append('result')
D['Descriptor']['properties']['completion_schema']=JSON
for _o in OPS:
    _o['scope_required']=['installation_id'] if 'scope' in _o['input_schema'].get('required',[]) else []
    if _o['id']=='_evidence.command.finish':
        _o['input_schema']=obj(command_id=ID,result=ref('Result'))
        _o['behavior']+=' Retain complete original result envelope including Fault message/details/retryability and cursor; replays return it exactly. Command ID must match result.command_id.'
