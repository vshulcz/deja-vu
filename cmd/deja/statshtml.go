package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/stats"
)

const statsHTMLCap = 5000

type htmlSession struct {
	Date     string `json:"date"`
	Harness  string `json:"harness"`
	Project  string `json:"project"`
	Title    string `json:"title"`
	Messages int    `json:"messages"`
}

type statsHTMLPage struct {
	TotalSessions int
	TotalMessages int
	Harnesses     int
	DateStart     string
	DateEnd       string
	Heatmap       stats.HeatmapStats
	PeakMonth     string
	PeakMessages  int
	SessionsJSON  template.JS
	SessionCount  int
	Truncated     bool
	// The sentence worth sharing, the same one the card prints. A page that
	// calls itself wrapped for sharing needs one line someone would quote.
	Punchline string
}

func writeStatsHTML(path string, report stats.Report, sessions []model.Session) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("stats html path: %w", err)
	}
	page, err := newStatsHTMLPage(report, sessions)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	if err := statsHTMLTemplate.Execute(&out, page); err != nil {
		return "", fmt.Errorf("render stats html: %w", err)
	}
	if err := os.WriteFile(abs, []byte(out.String()), 0o644); err != nil {
		return "", fmt.Errorf("cannot write %s — %s", path, writeFailureReason(err))
	}
	return abs, nil
}

func newStatsHTMLPage(report stats.Report, sessions []model.Session) (statsHTMLPage, error) {
	sessions = append([]model.Session(nil), sessions...)
	sort.SliceStable(sessions, func(i, j int) bool {
		left := sessions[i].Updated
		if left.IsZero() {
			left = sessions[i].Started
		}
		right := sessions[j].Updated
		if right.IsZero() {
			right = sessions[j].Started
		}
		if left.Equal(right) {
			return sessions[i].ID < sessions[j].ID
		}
		return left.After(right)
	})
	rows := make([]htmlSession, 0, len(sessions))
	for _, s := range sessions {
		date := s.Updated
		if date.IsZero() {
			date = s.Started
		}
		project := s.Project
		if project == "" {
			project = "-"
		}
		// The same scrub the terminal rows get: a project or a title carries
		// whatever the transcript held, and a bidi override reverses a line in
		// a browser too (#2090).
		rows = append(rows, htmlSession{
			Date: date.Format("2006-01-02"), Harness: s.Harness,
			Project: search.SafePath(project),
			Title:   search.SafeLine(stats.Title(s)), Messages: len(s.Messages),
		})
	}
	truncated := len(rows) > statsHTMLCap
	if truncated {
		rows = rows[:statsHTMLCap]
	}
	data, err := json.Marshal(rows)
	if err != nil {
		return statsHTMLPage{}, fmt.Errorf("encode stats html data: %w", err)
	}
	// the busiest month, so the heading carries a number instead of a range
	var peak stats.MonthStats
	for _, m := range report.Monthly {
		if m.Messages > peak.Messages {
			peak = m
		}
	}
	return statsHTMLPage{
		TotalSessions: report.TotalSessions, TotalMessages: report.TotalMessages,
		Harnesses: len(report.Harnesses), DateStart: report.DateRange.Start,
		DateEnd: report.DateRange.End, Heatmap: report.Heatmap,
		PeakMonth: peak.Month, PeakMessages: peak.Messages,
		SessionsJSON: template.JS(data), SessionCount: len(rows), Truncated: truncated,
		Punchline: cardPunchline(report),
	}, nil
}

var statsHTMLTemplate = template.Must(template.New("stats-html").Funcs(template.FuncMap{
	"heatOpacity": heatOpacity,
	"heatPct":     heatPct,
	"monthShort":  monthShort,
}).Parse(strings.Replace(statsHTMLSource, "{{MARK_ALIVE}}", markAlive(0, 0, 1), 1)))

// The mark is substituted before parsing rather than passed as data:
// html/template escapes data into an SVG context, and this markup is ours.
const statsHTMLSource = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>deja stats</title><link rel="icon" href="data:image/svg+xml,%3Csvg%20xmlns%3D%22http%3A%2F%2Fwww.w3.org%2F2000%2Fsvg%22%20viewBox%3D%220%200%2016%2016%22%3E%3Cpath%20fill%3D%22%238787af%22%20d%3D%22M1%201h2v1h-2ZM13%201h2v1h-2ZM1%202h3v1h-3ZM12%202h3v1h-3ZM1%203h4v1h-4ZM11%203h4v1h-4ZM1%204h14v1h-14ZM1%205h14v1h-14ZM1%206h3v1h-3ZM6%206h4v1h-4ZM12%206h3v1h-3ZM1%207h3v1h-3ZM6%207h4v1h-4ZM12%207h3v1h-3ZM1%208h3v1h-3ZM6%208h4v1h-4ZM12%208h3v1h-3ZM1%209h14v1h-14ZM1%2010h6v1h-6ZM9%2010h6v1h-6ZM1%2011h14v1h-14ZM1%2012h14v1h-14ZM2%2013h12v1h-12ZM3%2014h10v1h-10Z%22%2F%3E%3Cpath%20fill%3D%22%231c1c1c%22%20d%3D%22M4%206h2v1h-2ZM10%206h2v1h-2ZM4%207h2v1h-2ZM10%207h2v1h-2ZM4%208h2v1h-2ZM10%208h2v1h-2Z%22%2F%3E%3Cpath%20fill%3D%22%23ff8700%22%20d%3D%22M7%2010h2v1h-2Z%22%2F%3E%3C%2Fsvg%3E"><style>
:root{color-scheme:dark;--bg:#0b0f10;--panel:#12171a;--line:#1e262a;--text:#f4f7f7;--muted:#8b989a;--blue:#8787af;--green:#5ec27a;--orange:#ff8700}*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:14px ui-monospace,SFMono-Regular,Menlo,monospace}main{max-width:1100px;margin:auto;padding:38px 22px}h1{margin:0;color:var(--text);font-size:26px;letter-spacing:.5px}.hero{display:flex;gap:22px;align-items:center;margin:0 0 6px}.heroText{min-width:0}.punch{margin:6px 0 0;color:var(--orange);font-size:22px;font-weight:700;line-height:1.35}.sub{margin:6px 0 0;color:var(--muted);font-size:13px}.catwrap{flex:none}.cat{shape-rendering:crispEdges}.cat .eyes-shut,.cat .t1,.cat .t2{opacity:0}/* the blink: shut for a tenth of a cycle, which is about how long a cat's is */@keyframes dv-open{0%,92%{opacity:1}93%,97%{opacity:0}98%,100%{opacity:1}}@keyframes dv-shut{0%,92%{opacity:0}93%,97%{opacity:1}98%,100%{opacity:0}}.cat .eyes-open{animation:dv-open 6.5s steps(1,end) infinite}.cat .eyes-shut{animation:dv-shut 6.5s steps(1,end) infinite}/* the tail: three positions of the same cells, ping-ponged. The body never   moves, or it reads as a jitter rather than as a wag. */@keyframes dv-t0{0%,24.9%{opacity:1}25%,100%{opacity:0}}@keyframes dv-t1{0%,24.9%{opacity:0}25%,49.9%{opacity:1}50%,74.9%{opacity:0}75%,100%{opacity:1}}@keyframes dv-t2{0%,49.9%{opacity:0}50%,74.9%{opacity:1}75%,100%{opacity:0}}.cat .t0{animation:dv-t0 1.15s steps(1,end) infinite}.cat .t1{animation:dv-t1 1.15s steps(1,end) infinite}.cat .t2{animation:dv-t2 1.15s steps(1,end) infinite}@media (prefers-reduced-motion:reduce){  .cat .eyes-open,.cat .eyes-shut,.cat .t0,.cat .t1,.cat .t2{animation:none}  .cat .eyes-shut,.cat .t1,.cat .t2{opacity:0}}.mark{vertical-align:-5px;margin-right:12px;shape-rendering:crispEdges}p{color:var(--muted)}.range{float:right;color:var(--muted);font-size:13px}.totals{display:grid;grid-template-columns:repeat(3,1fr);gap:12px;margin:32px 0}.stat,.chart,.table-wrap{background:var(--panel);border:1px solid var(--line);border-radius:12px}.stat{padding:18px}.stat b{display:block;font-size:28px}.stat span{color:var(--muted)}h2{font-size:12px;color:var(--muted);letter-spacing:1.5px;margin:30px 0 12px}.chart{padding:18px 18px 14px}.heat{position:relative}.heatMonths{position:relative;height:14px;color:var(--muted);font-size:10px}.heatMonths span{position:absolute;top:0}.heatGrid{display:flex;gap:3px}.wk{display:flex;flex-direction:column;gap:3px;flex:1 1 0;min-width:0}.wk i{width:100%;aspect-ratio:1;border-radius:2px;background:var(--blue);display:block}.heatKey{display:flex;align-items:center;gap:4px;justify-content:flex-end;margin-top:10px;color:var(--muted);font-size:10px}.heatKey i{width:10px;height:10px;border-radius:2px;background:var(--blue)}.controls{display:flex;gap:10px;margin:12px 0}.controls input{width:100%;padding:11px;border-radius:8px;border:1px solid var(--line);background:var(--panel);color:var(--text);font:inherit}.table-wrap{overflow:auto}table{width:100%;border-collapse:collapse}th,td{text-align:left;padding:12px;border-bottom:1px solid var(--line);white-space:nowrap}th{color:var(--muted);font-size:11px}td.title{white-space:normal;min-width:260px}.badge{color:var(--green);cursor:pointer}.clickable{cursor:pointer}.empty{padding:20px;color:var(--muted)}footer{color:var(--muted);font-size:12px;margin-top:18px}@media(max-width:650px){main{padding:24px 12px}.range{float:none;display:block;margin-top:10px}.totals{grid-template-columns:1fr}.chart{gap:3px;padding:12px}}
</style></head><body><main><span class="range">{{if .DateStart}}{{.DateStart}} - {{.DateEnd}}{{else}}-{{end}}</span><div class="hero"><span class="catwrap"><svg class="cat" viewBox="0 0 24 22" width="96" height="88" aria-hidden="true">{{MARK_ALIVE}}</svg></span><div class="heroText"><h1>deja stats</h1><p class="punch">{{.Punchline}}</p><p class="sub">indexed agent work, wrapped for sharing</p></div></div>
<section class="totals"><div class="stat"><b>{{.TotalSessions}}</b><span>sessions</span></div><div class="stat"><b>{{.TotalMessages}}</b><span>messages</span></div><div class="stat"><b>{{.Harnesses}}</b><span>harnesses</span></div></section>
<h2>ACTIVITY / LAST 12 MONTHS{{if .PeakMonth}} &#183; BUSIEST {{.PeakMonth}}, {{.PeakMessages}} MESSAGES{{end}}</h2><div class="chart"><div class="heat" role="img" aria-label="Daily activity over the last year"><div class="heatMonths">{{range .Heatmap.Months}}<span style="left:{{heatPct .Col $.Heatmap.Weeks}}%">{{.Label}}</span>{{end}}</div><div class="heatGrid">{{range .Heatmap.Weeks}}<div class="wk">{{range .}}<i style="opacity:{{heatOpacity . $.Heatmap.Max}}"></i>{{end}}</div>{{end}}</div><div class="heatKey"><span>less</span><i style="opacity:.10"></i><i style="opacity:.35"></i><i style="opacity:.62"></i><i style="opacity:1"></i><span>more</span></div></div></div>
<h2>SESSIONS</h2><div class="controls"><input id="filter" type="search" placeholder="Filter sessions by harness, project, or title" aria-label="Filter sessions"></div><div class="table-wrap"><table><thead><tr><th>DATE</th><th>HARNESS</th><th>PROJECT</th><th>TITLE</th><th>MESSAGES</th></tr></thead><tbody id="sessions"></tbody></table><div id="empty" class="empty" hidden>No matching sessions.</div></div>
<footer>{{.SessionCount}} sessions embedded: dates, harnesses, projects, message counts, and each session's title or its opening line, redacted and shortened. No other message text is in this file.{{if .Truncated}} The embedded list is capped at the 5,000 most recent sessions.{{end}}</footer></main><script>
// Only metadata is embedded below: dates, harnesses, projects, counts, and redacted first-user titles. No message text.
const sessions={{.SessionsJSON}};const tbody=document.getElementById('sessions'),empty=document.getElementById('empty'),input=document.getElementById('filter');
// textContent leaves quotes alone, and two of the values below land inside a
// double-quoted attribute — a project name carrying a quote closed it and the
// rest of the name parsed as attributes, event handlers included. Project
// names arrive from "deja remember --project", from a directory name, and
// across "sync import" from another machine.
function esc(value){const node=document.createElement('span');node.textContent=value;return node.innerHTML.replace(/"/g,'&quot;').replace(/'/g,'&#39;')}function render(){const q=input.value.toLowerCase().trim();tbody.innerHTML='';let n=0;sessions.forEach((s,i)=>{const hay=[s.date,s.harness,s.project,s.title].join(' ').toLowerCase();if(q&&!hay.includes(q))return;n++;const row=document.createElement('tr');row.innerHTML='<td>'+esc(s.date)+'</td><td><span class="badge clickable" data-value="'+esc(s.harness)+'">'+esc(s.harness)+'</span></td><td><span class="clickable" data-value="'+esc(s.project)+'">'+esc(s.project)+'</span></td><td class="title">'+esc(s.title||'-')+'</td><td>'+s.messages+'</td>';row.querySelectorAll('.clickable').forEach(e=>e.onclick=()=>{input.value=e.dataset.value;render()});tbody.appendChild(row)});empty.hidden=n!==0}input.oninput=render;render();
</script></body></html>`

// heatOpacity maps a day's count onto four visible steps. A linear ramp
// against the busiest day flattens an ordinary week into nothing, because
// one outlier sets the scale for the whole year.
func heatOpacity(count, max int) string {
	if count <= 0 {
		return "0.06"
	}
	if max < 1 {
		max = 1
	}
	switch r := float64(count) / float64(max); {
	case r > 0.66:
		return "1"
	case r > 0.33:
		return "0.66"
	case r > 0.12:
		return "0.40"
	default:
		return "0.22"
	}
}

func monthShort(s string) string {
	if len(s) >= 7 {
		return s[5:]
	}
	return s
}

// heatPct is the left offset of a month label as a share of the grid. The
// columns stretch to whatever width the panel has, so a pixel offset could only
// be right at one window size — and gluing "0px" onto the index, as this did
// first, assumed a 10px pitch that never existed.
func heatPct(col int, weeks [][7]int) string {
	if len(weeks) == 0 {
		return "0"
	}
	return strconv.FormatFloat(float64(col)/float64(len(weeks))*100, 'f', 3, 64)
}
