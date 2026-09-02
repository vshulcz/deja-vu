import json, subprocess, time, os, sys
import sys
S=sys.argv[1]  # the -keep directory from day0bench
env=dict(os.environ, HOME=f'{S}/day0/home', CASS_DATA_DIR=f'{S}/cassdata', CODING_AGENT_SEARCH_NO_UPDATE_PROMPT='1', TUI_HEADLESS='1')
cass=os.environ.get('CASS', 'cass')
t0=time.time()
r=subprocess.run([cass,'index','--full','--json'],env=env,capture_output=True,text=True)
build=time.time()-t0
print('index rc',r.returncode,'build_s',round(build,1)); print(r.stdout[-600:]); print(r.stderr[-400:])
qs=json.load(open(f'{S}/day0/questions.json'))
hit1=hit5=found=0; first=None; lat=[]
for i,q in enumerate(qs):
    t1=time.time()
    r=subprocess.run([cass,'search',q['question'],'--robot','--limit','50','--fields','source_path'],env=env,capture_output=True,text=True)
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
