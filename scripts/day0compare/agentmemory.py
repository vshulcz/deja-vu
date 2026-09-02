import json, subprocess, time, os, urllib.request
import sys
S=sys.argv[1]  # the -keep directory from day0bench
env=dict(os.environ, HOME=f'{S}/amhome', AGENTMEMORY_DATA_DIR=f'{S}/amdata')
AM=os.environ.get('AGENTMEMORY', 'agentmemory')
batches=sorted(d for d in os.listdir(f'{S}/am-batches') if d.startswith('b'))
t0=time.time(); files=0
for b in batches:
    if b=='b00': continue  # already imported
    r=subprocess.run([AM,'import-jsonl',f'{S}/am-batches/{b}','--max-files=1000'],env=env,capture_output=True,text=True)
    m=[l for l in r.stdout.splitlines() if 'imported' in l]
    print(b, m[-1][-80:] if m else r.stderr[-200:])
build=time.time()-t0
print('import wall_s', round(build,1), 'batches', len(batches))
def search(q, limit=50):
    req=urllib.request.Request('http://localhost:3111/agentmemory/search', data=json.dumps({'query':q,'limit':limit,'format':'full'}).encode(), headers={'content-type':'application/json'})
    return json.load(urllib.request.urlopen(req, timeout=120))
qs=json.load(open(f'{S}/day0/questions.json'))
hit1=hit5=found=0; first=None; lat=[]
for i,q in enumerate(qs):
    t1=time.time(); d=search(q['question']); dt=time.time()-t1; lat.append(dt)
    if first is None: first=dt
    want=set(q['answer_session_ids'])
    order=[]
    for r in d.get('results',[]):
        sid=r.get('sessionId') or (r.get('observation') or {}).get('sessionId')
        if sid and sid not in order: order.append(sid)
    rank=next((k for k,sid in enumerate(order) if sid in want), None)
    if rank is not None:
        found+=1
        if rank==0: hit1+=1
        if rank<5: hit5+=1
lat.sort()
print(f'agentmemory import {build:.1f}s(+5s b00) first {first*1000:.0f}ms p50 {lat[len(lat)//2]*1000:.0f}ms hit@1 {hit1}/{len(qs)} hit@5 {hit5}/{len(qs)} found@50 {found}/{len(qs)}')
