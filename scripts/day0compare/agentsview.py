import json, subprocess, time, os, sys, re
S=sys.argv[1]  # the -keep directory from day0bench
env=dict(os.environ, HOME=f'{S}/day0/home', AGENTSVIEW_DATA_DIR=os.environ.get('AGENTSVIEW_DATA_DIR', os.path.join(S, 'agentsview-data')), AGENTSVIEW_DISABLE_UPDATE_CHECK='1')
av=os.environ.get('AGENTSVIEW', 'agentsview')
t0=time.time()
if not os.environ.get('SKIP_INDEX'):
    r=subprocess.run([av,'sync'],env=dict(env, AGENTSVIEW_NO_DAEMON='1'),capture_output=True,text=True)
    print('sync rc',r.returncode,'build_s',round(time.time()-t0,1)); print(r.stdout[-600:]); print(r.stderr[-400:])
# search goes through the daemon
subprocess.run([av,'daemon','start'],env=env,capture_output=True,text=True)
qs=json.load(open(f'{S}/day0/questions.json'))
STOP=set('a an the of on in at to for with and or is are was were did do does i my me you your it its what where when how which who whom whose that this these those have has had been be am from by as into about over under after before than then there here'.split())
def keywords(q):
    toks=[t for t in re.findall(r"[A-Za-z0-9$']+", q.lower()) if t not in STOP]
    return ' '.join(toks) or q
KEYWORDS=bool(os.environ.get('KEYWORDS'))
mode=sys.argv[2:]  # e.g. --fts
hit1=hit5=found=empty=0; first=None; lat=[]
try:
    for q in qs:
        t1=time.time()
        r=subprocess.run([av,'session','search',keywords(q['question']) if KEYWORDS else q['question'],'--limit','500','--json']+mode,env=env,capture_output=True,text=True)
        dt=time.time()-t1; lat.append(dt)
        if first is None: first=dt
        try:
            matches=json.loads(r.stdout).get('matches') or []
        except Exception:
            print('bad json', r.stdout[:200], r.stderr[:200]); matches=[]
        if not matches: empty+=1
        # results are messages; rank the sessions they belong to
        seen=[]
        for m in matches:
            sid=m.get('session_id','')
            if sid.startswith('codex:rollout-'): sid=sid[len('codex:rollout-'):]
            if sid and sid not in seen: seen.append(sid)
        want=set(q['answer_session_ids'])
        rank=next((k for k,s in enumerate(seen[:50]) if s in want),None)
        if rank is not None:
            found+=1
            if rank==0: hit1+=1
            if rank<5: hit5+=1
finally:
    subprocess.run([av,'daemon','stop'],env=env,capture_output=True,text=True)
lat.sort()
print(f'agentsview {" ".join(mode) or "default"}{" keywords" if KEYWORDS else ""} first {first*1000:.0f}ms p50 {lat[len(lat)//2]*1000:.0f}ms hit@1 {hit1}/{len(qs)} hit@5 {hit5}/{len(qs)} found@50 {found}/{len(qs)} empty {empty}')
