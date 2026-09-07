# Score Hugging Face funes over the day0bench corpus: `funes index` the Claude
# layout (the same sessions sit under both roots), then run every question
# through `funes recall` and look for the answer session's id in the result
# headers, in rank order. Usage:
#   FUNES=/path/to/funes python3 funes.py <keep-dir> [--half-life N]
# The header line of a hit shortens the id to eight characters; the full one
# is on the line under it:
#   → get answer_355c48bb --from 0 --to 3 --memory …
import json, os, re, subprocess, sys, time
S = sys.argv[1]
FUNES = os.environ.get('FUNES', 'funes')
half = None
if '--half-life' in sys.argv:
    half = sys.argv[sys.argv.index('--half-life') + 1]
home = os.path.join(S, 'home')
env = dict(os.environ, HOME=home)
corpus = os.path.join(home, '.claude', 'projects')
memory = os.path.join(home, '.funes', 'memory')
t0 = time.time()
if not os.path.exists(memory):
    r = subprocess.run([FUNES, 'index', corpus, '--harness', 'claude', '--yes', '--no-thinking'],
                       capture_output=True, text=True, env=env)
    print('index rc', r.returncode, (r.stdout + r.stderr)[-400:])
build = time.time() - t0
qs = json.load(open(os.path.join(S, 'questions.json')))
hit1 = hit5 = found = 0; first = None; lat = []
head = re.compile(r'^\s*→ get (\S+) --from', re.M)
for q in qs:
    args = [FUNES, 'recall', q['question'], '-k', '50', '--candidates', '50']
    if half is not None:
        args += ['--half-life', half]
    t1 = time.time()
    r = subprocess.run(args, capture_output=True, text=True, env=env)
    dt = time.time() - t1; lat.append(dt)
    if first is None: first = dt
    want = set(q['answer_session_ids'])
    order = []
    for m in head.finditer(r.stdout):
        sid = m.group(1)
        if sid not in order: order.append(sid)
    rank = next((k for k, sid in enumerate(order) if sid in want), None)
    if rank is not None:
        found += 1
        if rank == 0: hit1 += 1
        if rank < 5: hit5 += 1
lat.sort()
print(f'funes index {build:.0f}s first {first*1000:.0f}ms p50 {lat[len(lat)//2]*1000:.0f}ms '
      f'hit@1 {hit1}/{len(qs)} hit@5 {hit5}/{len(qs)} found@50 {found}/{len(qs)} half-life {half or "default"}')
