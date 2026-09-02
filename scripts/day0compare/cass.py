import json, subprocess, time, os, sys
import sys
S=sys.argv[1]  # the -keep directory from day0bench
env=dict(os.environ, HOME=f'{S}/day0/home', CASS_DATA_DIR=os.environ.get('CASS_DATA_DIR', os.path.join(S, 'cass-data')), CODING_AGENT_SEARCH_NO_UPDATE_PROMPT='1', TUI_HEADLESS='1')
cass=os.environ.get('CASS', 'cass')
t0=time.time()
if os.environ.get('SKIP_INDEX'):
    r=subprocess.CompletedProcess([],0,'','')
else:
    r=subprocess.run([cass,'index','--full','--json'],env=env,capture_output=True,text=True)
build=time.time()-t0
print('index rc',r.returncode,'build_s',round(build,1)); print(r.stdout[-600:]); print(r.stderr[-400:])
qs=json.load(open(f'{S}/day0/questions.json'))
STOP=set('a an the of on in at to for with and or is are was were did do does i my me you your it its what where when how which who whom whose that this these those have has had been be am from by as into about over under after before than then there here'.split())
import re as _re
def keywords(q):
    toks=[t for t in _re.findall(r"[A-Za-z0-9$']+", q.lower()) if t not in STOP]
    return ' '.join(toks) or q
KEYWORDS=bool(os.environ.get('KEYWORDS'))
hit1=hit5=found=0; first=None; lat=[]
for i,q in enumerate(qs):
    t1=time.time()
    r=subprocess.run([cass,'search',keywords(q['question']) if KEYWORDS else q['question'],'--robot','--limit','50','--fields','source_path','--mode','lexical'],env=env,capture_output=True,text=True)
    dt=time.time()-t1; lat.append(dt)
    if first is None: first=dt
    try:
        d=json.loads(r.stdout)
    except Exception:
        print('bad json', r.stdout[:200], r.stderr[:200]); continue
    hits=d.get('hits') or d.get('results') or d
    want=set(q['answer_session_ids'])
    rank=None
    for k,h in enumerate(hits if isinstance(hits,list) else []):
        sp=h.get('source_path','')
        base=os.path.basename(sp)
        sid=base.replace('rollout-','').replace('.jsonl','')
        if sid in want: rank=k; break
    if rank is not None:
        found+=1
        if rank==0: hit1+=1
        if rank<5: hit5+=1
lat.sort()
print(f'cass build {build:.1f}s first {first*1000:.0f}ms p50 {lat[len(lat)//2]*1000:.0f}ms hit@1 {hit1}/{len(qs)} hit@5 {hit5}/{len(qs)} found@50 {found}/{len(qs)}')
