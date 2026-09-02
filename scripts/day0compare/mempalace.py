# Score MemPalace over the day0bench corpus: mine the Claude layout, then run
# every question through `mempalace search` and look for the answer session's
# id in the "Source:" lines, in rank order. Usage:
#   python3 mempalace.py <keep-dir> [palace-dir]
import json, os, re, subprocess, sys, time
S = sys.argv[1]
palace = sys.argv[2] if len(sys.argv) > 2 else os.path.join(S, 'mempalace-palace')
MP = os.environ.get('MEMPALACE', 'mempalace')
corpus = os.path.join(S, 'home', '.claude', 'projects', '-work-day0')
t0 = time.time()
if not os.path.exists(palace):
    r = subprocess.run([MP, '--palace', palace, 'mine', '--mode', 'convos', corpus], capture_output=True, text=True)
    print('mine rc', r.returncode, r.stdout[-300:])
build = time.time() - t0
qs = json.load(open(os.path.join(S, 'questions.json')))
hit1 = hit5 = found = 0; first = None; lat = []
src = re.compile(r'^\s*Source:\s+(\S+)', re.M)
for q in qs:
    t1 = time.time()
    r = subprocess.run([MP, '--palace', palace, 'search', q['question'], '--results', '50'], capture_output=True, text=True)
    dt = time.time() - t1; lat.append(dt)
    if first is None: first = dt
    want = set(q['answer_session_ids'])
    # MemPalace splits a file into drawers named <id>_<n>.jsonl; an id may
    # itself carry underscores (ultrachat_440010), so match by prefix rather
    # than by stripping a suffix.
    order = []
    for m in src.finditer(r.stdout):
        base = os.path.basename(m.group(1))
        sid = next((w for w in want if base == w + '.jsonl' or re.match(re.escape(w) + r'_\d+\.jsonl$', base)), None)
        key = sid or re.sub(r'\.jsonl$', '', base)
        if key not in order: order.append(key)
    rank = next((k for k, sid in enumerate(order) if sid in want), None)
    if rank is not None:
        found += 1
        if rank == 0: hit1 += 1
        if rank < 5: hit5 += 1
lat.sort()
print(f'mempalace mine {build:.0f}s first {first*1000:.0f}ms p50 {lat[len(lat)//2]*1000:.0f}ms hit@1 {hit1}/{len(qs)} hit@5 {hit5}/{len(qs)} found@50 {found}/{len(qs)}')
