package main

import (
	"html/template"
	"strings"
)

// viewTemplate is the whole viewer: one self-contained dark-terminal page,
// no external assets, all data embedded as JSON and filtered client-side.
// The mark is substituted into the source before parsing rather than passed as
// data: html/template escapes data into an SVG context, and this markup is ours,
// not a value.
var viewTemplate = template.Must(template.New("view").Parse(strings.Replace(viewSource, "{{MARK_STILL}}", markStill(0, 0, 1), 1)))

const viewSource = `<!doctype html>
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
header{position:sticky;top:0;background:rgba(5,8,7,.95);border-bottom:1px solid var(--line);padding:12px 22px;display:flex;gap:18px;align-items:center;flex-wrap:wrap;z-index:5}
header b{color:var(--ph)}
header .meta{color:var(--faint);font-size:.8rem}
header .star{margin-left:auto;border:1px solid var(--ph);color:var(--hi);background:rgba(135,135,175,.14);padding:4px 11px;border-radius:6px;font-size:.78rem;font-weight:600}
header .star:hover{background:var(--ph);color:var(--bg)}
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
.map-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(310px,1fr));gap:12px;margin:4px 0 14px}
.map-card{border:1px solid var(--line);background:var(--panel);padding:12px 14px;cursor:pointer;transition:border-color .15s}
.map-card:hover{border-color:var(--ph)}
.mc-hd{display:flex;justify-content:space-between;align-items:baseline;gap:8px}
.mc-p{color:var(--hi);font-size:.92rem;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.mc-cnt{color:var(--faint);font-size:.75rem;white-space:nowrap}
.mc-bar{display:flex;height:6px;border-radius:3px;overflow:hidden;background:var(--line);margin:8px 0}
.mc-tags{display:flex;gap:8px;flex-wrap:wrap;font-size:.74rem;margin-bottom:8px}
.mc-n,.mc-s{font-size:.76rem;color:var(--body);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;padding:2px 0;border-top:1px dashed var(--line)}
.note{color:var(--faint);font-size:.78rem;margin:10px 0 30px}
.empty{color:var(--faint);padding:24px 0}
</style></head><body>
<header><b><svg class="mark" viewBox="0 0 24 22" width="22" height="20" aria-hidden="true">{{MARK_STILL}}</svg>deja view</b><span class="meta">generated {{.GeneratedAt}} · local file, nothing leaves this machine</span><a class="star" href="https://github.com/vshulcz/deja-vu">★ Star on GitHub</a></header>
<div class="wrap">
<div class="stats">
<div class="stat"><b>{{.TotalSessions}}</b><span>sessions</span></div>
<div class="stat"><b>{{.Harnesses}}</b><span>agents</span></div>
<div class="stat"><b>{{if .DateStart}}{{.DateStart}} → {{.DateEnd}}{{else}}—{{end}}</b><span>covered</span></div>
</div>
<div class="tabs">
<button class="on" data-tab="sessions">Sessions</button>
<button data-tab="map">Map</button>
<button data-tab="recalls">Recalls</button>
<button data-tab="notes">Notes</button>
</div>
<div id="tab-sessions">
<input type="search" id="q" placeholder="filter sessions — title, project, harness, preview text">
<div id="list"></div>
<p class="note">{{if .TotalSessions}}previews embedded for the {{.PreviewCount}} most recent sessions and capped — full-text search lives in <b>deja "query"</b> and the agents' recall tool.{{end}}{{if .SessionsWithheld}} Held back by the trust policy ({{.PolicyRule}}): {{.SessionsWithheld}}.{{end}}</p>
</div>
<div id="tab-map" style="display:none"><div id="mlist"></div>
<p class="note">cross-agent project map — shows which harnesses worked in each project and promoted decisions attached to it. Click any project card to filter its sessions.</p></div>
<div id="tab-recalls" style="display:none"><div id="rlist"></div>
<p class="note">the injections agents received, verbatim — the audit trail behind <b>deja log</b>.{{if lt .RecallCount .TotalRecalls}} The {{.RecallCount}} most recent of {{.TotalRecalls}} are on this page; older ones stay in the log until it rotates.{{end}}{{if .RecallsWithheld}} Held back by the trust policy ({{.PolicyRule}}): {{.RecallsWithheld}}.{{end}}</p></div>
<div id="tab-notes" style="display:none"><div id="nlist"></div>
<p class="note">decisions promoted with <b>deja promote</b>; lifecycle states shown as badges. A plain <b>deja remember</b> note is indexed as a session and reads on the Sessions tab.{{if lt .NoteCount .TotalNotes}} The {{.NoteCount}} most recent notes of {{.TotalNotes}} are on this page — the rest answer through <b>deja "query"</b> and the agents' recall tool.{{end}}{{if .NotesWithheld}} Held back by the trust policy ({{.PolicyRule}}): {{.NotesWithheld}}.{{end}}</p></div>
</div>
<script>
const S={{.SessionsJSON}},R={{.RecallsJSON}},N={{.NotesJSON}};
const HC={{.HarnessColorsJSON}};
// Quotes included: rowN puts a value inside a double-quoted class attribute,
// and every row here is built by concatenation, so an escaper that stops at
// &<> is one attribute away from letting a project or note field close it.
const esc=s=>(s||'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
function rowS(s){return '<div class="row" onclick="this.classList.toggle(\'open\')"><span class="h" style="color:'+esc(HC[s.harness]||'')+'">['+esc(s.harness)+']</span> <span class="t">'+esc(s.title||s.id)+'</span> <span class="m">'+esc(s.project)+' · '+esc(s.updated)+'</span>'+(s.preview?'<pre>'+esc(s.preview)+'</pre>':'')+'</div>'}
function rowR(r){return '<div class="row" onclick="this.classList.toggle(\'open\')"><span class="h">['+esc(r.kind)+']</span> <span class="t">'+r.sessions+' sessions · '+r.bytes+' B</span> <span class="m">'+esc(r.time)+(r.policy?' · '+esc(r.policy):'')+(r.terms&&r.terms.length?' · via: '+esc(r.terms.join(', ')):'')+'</span><pre>'+esc(r.digest)+'</pre></div>'}
// The decision heads the row. A note's title is the session's own opening line
// — the question — so leading with it put an "accepted" badge on the question
// (#2460). The title stays under it, as what the decision answers.
function rowN(n){var head=n.text||n.title,under=(n.text&&n.title)?n.title:'';return '<div class="row"><span class="badge '+esc(n.state)+'">'+esc(n.state)+'</span> <span class="t">'+esc(head)+'</span> <span class="m">'+esc(n.project)+' · '+esc(n.at)+(n.tags&&n.tags.length?' · #'+esc(n.tags.join(' #')):'')+'</span>'+(under?'<pre style="display:block">'+esc(under)+'</pre>':'')+'</div>'}
function draw(){const q=(document.getElementById('q').value||'').toLowerCase();
const hit=S.filter(s=>!q||[s.title,s.project,s.harness,s.preview,s.id].join(' ').toLowerCase().includes(q));
document.getElementById('list').innerHTML=hit.length?hit.map(rowS).join(''):q?'<div class="empty">nothing matches — try deja "'+esc(q)+'" for full-text search</div>':'<div class="empty">no sessions indexed yet — <code>deja sources</code> shows where deja looked</div>'}
document.getElementById('q').addEventListener('input',draw);
document.getElementById('rlist').innerHTML=R.length?R.map(rowR).join(''):'<div class="empty">no recalls recorded yet</div>';
document.getElementById('nlist').innerHTML=N.length?N.map(rowN).join(''):'<div class="empty">no curated notes yet — deja promote &lt;id&gt;</div>';
function drawMap(){const pm={};S.forEach(s=>{if(s.harness==='deja')return;const k=s.project||'(unscoped)';if(!pm[k])pm[k]={name:k,s:[],n:[],h:{}};pm[k].s.push(s);pm[k].h[s.harness]=(pm[k].h[s.harness]||0)+1});N.forEach(n=>{const k=n.project||'(unscoped)';if(!pm[k])pm[k]={name:k,s:[],n:[],h:{}};pm[k].n.push(n)});const ps=Object.values(pm).sort((a,b)=>(b.s.length+b.n.length*2)-(a.s.length+a.n.length*2));document.getElementById('mlist').innerHTML=ps.length?'<div class="map-grid">'+ps.map(p=>{const hs=Object.entries(p.h).sort((a,b)=>b[1]-a[1]);const tot=p.s.length||1;const bar=hs.map(([h,c])=>'<span style="width:'+(c*100/tot).toFixed(1)+'%;background:'+esc(HC[h]||'#8787af')+'"></span>').join('');const tags=hs.map(([h,c])=>'<span style="color:'+esc(HC[h]||'')+'">['+esc(h)+'] ×'+c+'</span>').join('');const ns=p.n.slice(0,2).map(n=>'<div class="mc-n"><span class="badge '+esc(n.state)+'">'+esc(n.state)+'</span> '+esc(n.text||n.title)+'</div>').join('');const seen={};const ss=p.s.filter(s=>{const t=s.title||s.id;if(seen[t])return false;seen[t]=1;return true}).slice(0,2).map(s=>'<div class="mc-s"><span style="color:'+esc(HC[s.harness]||'')+'">['+esc(s.harness)+']</span> '+esc(s.title||s.id)+'</div>').join('');return '<div class="map-card" data-proj="'+esc(p.name)+'"><div class="mc-hd"><b class="mc-p">'+esc(p.name)+'</b><span class="mc-cnt">'+p.s.length+(p.s.length===1?' session':' sessions')+(p.n.length?' · '+p.n.length+(p.n.length===1?' note':' notes'):'')+'</span></div><div class="mc-bar">'+bar+'</div><div class="mc-tags">'+tags+'</div>'+ns+ss+'</div>'}).join('')+'</div>':'<div class="empty">no projects mapped yet</div>';document.querySelectorAll('.map-card').forEach(c=>c.addEventListener('click',()=>{document.getElementById('q').value=c.dataset.proj==="(unscoped)"?"":c.dataset.proj;draw();document.querySelector('.tabs button[data-tab="sessions"]').click()}))}
drawMap();
document.querySelectorAll('.tabs button').forEach(b=>b.addEventListener('click',()=>{
document.querySelectorAll('.tabs button').forEach(x=>x.classList.remove('on'));b.classList.add('on');
['sessions','map','recalls','notes'].forEach(t=>document.getElementById('tab-'+t).style.display=t===b.dataset.tab?'':'none')}));
draw();
const hTab=location.hash.slice(1);if(['sessions','map','recalls','notes'].includes(hTab))document.querySelector('.tabs button[data-tab="'+hTab+'"]').click();
</script></body></html>
`
