package main

import "html/template"

// viewTemplate is the whole viewer: one self-contained dark-terminal page,
// no external assets, all data embedded as JSON and filtered client-side.
var viewTemplate = template.Must(template.New("view").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>deja view</title>
<style>
:root{--bg:#050807;--ph:#4af08b;--hi:#8affc0;--amber:#ffb454;--body:#a9cbb6;--faint:#5d8a6e;--line:#12291c;--panel:#070d0a}
*{margin:0;padding:0;box-sizing:border-box}
body{background:var(--bg);color:var(--body);font:14px/1.6 "SF Mono","JetBrains Mono",Menlo,Consolas,monospace;padding:0 0 60px}
a{color:var(--ph);text-decoration:none}
header{position:sticky;top:0;background:rgba(5,8,7,.95);border-bottom:1px solid var(--line);padding:12px 22px;display:flex;gap:18px;align-items:baseline;flex-wrap:wrap;z-index:5}
header b{color:var(--ph)}
header .meta{color:var(--faint);font-size:.8rem}
.wrap{max-width:1100px;margin:0 auto;padding:0 22px}
.stats{display:flex;gap:26px;flex-wrap:wrap;margin:20px 0}
.stat b{display:block;color:var(--hi);font-size:1.25rem}
.stat span{color:var(--faint);font-size:.78rem}
.tabs{display:flex;gap:2px;margin:8px 0 14px;border-bottom:1px solid var(--line)}
.tabs button{background:none;border:none;color:var(--faint);font:inherit;padding:8px 14px;cursor:pointer;border-bottom:2px solid transparent}
.tabs button.on{color:var(--ph);border-bottom-color:var(--ph)}
input[type=search]{width:100%;background:var(--panel);border:1px solid var(--line);color:var(--body);font:inherit;padding:9px 12px;margin:0 0 12px}
input[type=search]:focus{outline:none;border-color:var(--ph)}
.row{border:1px solid var(--line);border-top:none;padding:9px 12px;cursor:pointer}
.row:first-of-type{border-top:1px solid var(--line)}
.row:hover{background:var(--panel)}
.row .t{color:#d7f5e2}
.row .m{color:var(--faint);font-size:.78rem}
.row .h{color:var(--amber)}
.row pre{display:none;white-space:pre-wrap;color:var(--body);border-top:1px dashed var(--line);margin-top:8px;padding-top:8px;font-size:.82rem;max-height:420px;overflow:auto}
.row.open pre{display:block}
.badge{border:1px solid var(--line);padding:0 6px;font-size:.72rem;color:var(--faint)}
.badge.accepted{color:var(--ph)}.badge.rejected{color:#e2604a}.badge.superseded,.badge.stale{color:var(--amber)}
.note{color:var(--faint);font-size:.78rem;margin:10px 0 30px}
.empty{color:var(--faint);padding:24px 0}
</style></head><body>
<header><b>deja view</b><span class="meta">generated {{.GeneratedAt}} · local file, nothing leaves this machine</span></header>
<div class="wrap">
<div class="stats">
<div class="stat"><b>{{.TotalSessions}}</b><span>sessions</span></div>
<div class="stat"><b>{{.Harnesses}}</b><span>agents</span></div>
<div class="stat"><b>{{.DateStart}} → {{.DateEnd}}</b><span>covered</span></div>
</div>
<div class="tabs">
<button class="on" data-tab="sessions">Sessions</button>
<button data-tab="recalls">Recalls</button>
<button data-tab="notes">Notes</button>
</div>
<div id="tab-sessions">
<input type="search" id="q" placeholder="filter sessions — title, project, harness, preview text">
<div id="list"></div>
<p class="note">previews embedded for the {{.PreviewCount}} most recent sessions and capped — full-text search lives in <b>deja "query"</b> and the agents' recall tool.</p>
</div>
<div id="tab-recalls" style="display:none"><div id="rlist"></div>
<p class="note">every injection an agent received, verbatim — the audit trail behind <b>deja log</b>.</p></div>
<div id="tab-notes" style="display:none"><div id="nlist"></div>
<p class="note">curated notes from <b>deja promote</b> / <b>deja remember</b>; lifecycle states shown as badges.</p></div>
</div>
<script>
const S={{.SessionsJSON}},R={{.RecallsJSON}},N={{.NotesJSON}};
const esc=s=>(s||'').replace(/[&<>]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;'}[c]));
function rowS(s){return '<div class="row" onclick="this.classList.toggle(\'open\')"><span class="h">['+esc(s.harness)+']</span> <span class="t">'+esc(s.title||s.id)+'</span> <span class="m">'+esc(s.project)+' · '+esc(s.updated)+'</span>'+(s.preview?'<pre>'+esc(s.preview)+'</pre>':'')+'</div>'}
function rowR(r){return '<div class="row" onclick="this.classList.toggle(\'open\')"><span class="h">['+esc(r.kind)+']</span> <span class="t">'+r.sessions+' sessions · '+r.bytes+' B</span> <span class="m">'+esc(r.time)+(r.policy?' · '+esc(r.policy):'')+(r.terms&&r.terms.length?' · via: '+esc(r.terms.join(', ')):'')+'</span><pre>'+esc(r.digest)+'</pre></div>'}
function rowN(n){return '<div class="row"><span class="badge '+esc(n.state)+'">'+esc(n.state)+'</span> <span class="t">'+esc(n.title)+'</span> <span class="m">'+esc(n.project)+' · '+esc(n.at)+(n.tags&&n.tags.length?' · #'+esc(n.tags.join(' #')):'')+'</span><pre style="display:block">'+esc(n.text)+'</pre></div>'}
function draw(){const q=(document.getElementById('q').value||'').toLowerCase();
const hit=S.filter(s=>!q||[s.title,s.project,s.harness,s.preview,s.id].join(' ').toLowerCase().includes(q));
document.getElementById('list').innerHTML=hit.length?hit.map(rowS).join(''):'<div class="empty">nothing matches — try deja "'+esc(q)+'" for full-text search</div>'}
document.getElementById('q').addEventListener('input',draw);
document.getElementById('rlist').innerHTML=R.length?R.map(rowR).join(''):'<div class="empty">no recalls recorded yet</div>';
document.getElementById('nlist').innerHTML=N.length?N.map(rowN).join(''):'<div class="empty">no curated notes yet — deja promote &lt;id&gt;</div>';
document.querySelectorAll('.tabs button').forEach(b=>b.addEventListener('click',()=>{
document.querySelectorAll('.tabs button').forEach(x=>x.classList.remove('on'));b.classList.add('on');
['sessions','recalls','notes'].forEach(t=>document.getElementById('tab-'+t).style.display=t===b.dataset.tab?'':'none')}));
draw();
</script></body></html>
`))
