package main

import "html/template"

// viewTemplate is the whole viewer: one self-contained dark-terminal page,
// no external assets, all data embedded as JSON and filtered client-side.
var viewTemplate = template.Must(template.New("view").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>deja view</title>
<link rel="icon" href="data:image/svg+xml,%3Csvg%20xmlns%3D%22http%3A%2F%2Fwww.w3.org%2F2000%2Fsvg%22%20viewBox%3D%220%200%2016%2016%22%3E%3Cpath%20fill%3D%22%238787af%22%20d%3D%22M1%201h2v1h-2ZM13%201h2v1h-2ZM1%202h3v1h-3ZM12%202h3v1h-3ZM1%203h4v1h-4ZM11%203h4v1h-4ZM1%204h14v1h-14ZM1%205h14v1h-14ZM1%206h3v1h-3ZM6%206h4v1h-4ZM12%206h3v1h-3ZM1%207h3v1h-3ZM6%207h4v1h-4ZM12%207h3v1h-3ZM1%208h3v1h-3ZM6%208h4v1h-4ZM12%208h3v1h-3ZM1%209h14v1h-14ZM1%2010h6v1h-6ZM9%2010h6v1h-6ZM1%2011h14v1h-14ZM1%2012h14v1h-14ZM2%2013h12v1h-12ZM3%2014h10v1h-10Z%22%2F%3E%3Cpath%20fill%3D%22%231c1c1c%22%20d%3D%22M4%206h2v1h-2ZM10%206h2v1h-2ZM4%207h2v1h-2ZM10%207h2v1h-2ZM4%208h2v1h-2ZM10%208h2v1h-2Z%22%2F%3E%3Cpath%20fill%3D%22%23ff8700%22%20d%3D%22M7%2010h2v1h-2Z%22%2F%3E%3C%2Fsvg%3E">
<style>
.mark{vertical-align:-3px;margin-right:7px;shape-rendering:crispEdges}
:root{--bg:#0b0f10;--ph:#8787af;--hi:#f4f7f7;--amber:#ff8700;--body:#c3ccce;--faint:#55626a;--line:#1e262a;--panel:#12171a}
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
.row .t{color:#f4f7f7}
.row .m{color:var(--faint);font-size:.78rem}
.row .h{color:var(--amber)}
.row pre{display:none;white-space:pre-wrap;color:var(--body);border-top:1px dashed var(--line);margin-top:8px;padding-top:8px;font-size:.82rem;max-height:420px;overflow:auto}
.row.open pre{display:block}
.badge{border:1px solid var(--line);padding:0 6px;font-size:.72rem;color:var(--faint)}
.badge.accepted{color:var(--ph)}.badge.rejected{color:#e2604a}.badge.superseded,.badge.stale{color:var(--amber)}
.note{color:var(--faint);font-size:.78rem;margin:10px 0 30px}
.empty{color:var(--faint);padding:24px 0}
</style></head><body>
<header><b><svg class="mark" viewBox="0 0 24 22" width="22" height="20" aria-hidden="true"><path fill="#8787af" d="M4 0h1v1h-1ZM17 0h1v1h-1ZM3 1h3v1h-3ZM16 1h3v1h-3ZM3 2h4v1h-4ZM15 2h4v1h-4ZM3 3h5v1h-5ZM14 3h5v1h-5ZM3 4h16v1h-16ZM2 5h18v1h-18ZM2 6h18v1h-18ZM2 7h3v1h-3ZM7 7h8v1h-8ZM17 7h3v1h-3ZM2 8h3v1h-3ZM7 8h8v1h-8ZM17 8h3v1h-3ZM2 9h3v1h-3ZM7 9h8v1h-8ZM17 9h3v1h-3ZM2 10h18v1h-18ZM2 11h8v1h-8ZM12 11h8v1h-8ZM2 12h18v1h-18ZM3 13h16v1h-16ZM4 14h14v1h-14ZM19 14h2v1h-2ZM5 15h12v1h-12ZM19 15h2v1h-2ZM5 16h12v1h-12ZM19 16h2v1h-2ZM5 17h12v1h-12ZM19 17h2v1h-2ZM5 18h12v1h-12ZM19 18h2v1h-2ZM4 19h16v1h-16ZM4 20h16v1h-16ZM5 21h4v1h-4ZM13 21h4v1h-4Z"/><path fill="#1c1c1c" d="M5 7h2v1h-2ZM15 7h2v1h-2ZM5 8h2v1h-2ZM15 8h2v1h-2ZM5 9h2v1h-2ZM15 9h2v1h-2Z"/><path fill="#ff8700" d="M10 11h2v1h-2Z"/></svg>deja view</b><span class="meta">generated {{.GeneratedAt}} · local file, nothing leaves this machine</span></header>
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
// Quotes included: rowN puts a value inside a double-quoted class attribute,
// and every row here is built by concatenation, so an escaper that stops at
// &<> is one attribute away from letting a project or note field close it.
const esc=s=>(s||'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
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
