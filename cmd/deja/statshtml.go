package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
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
	Monthly       []stats.MonthStats
	SessionsJSON  template.JS
	SessionCount  int
	Truncated     bool
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
		return "", fmt.Errorf("write stats html: %w", err)
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
		rows = append(rows, htmlSession{
			Date: date.Format("2006-01-02"), Harness: s.Harness, Project: project,
			Title: stats.Title(s), Messages: len(s.Messages),
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
	return statsHTMLPage{
		TotalSessions: report.TotalSessions, TotalMessages: report.TotalMessages,
		Harnesses: len(report.Harnesses), DateStart: report.DateRange.Start,
		DateEnd: report.DateRange.End, Monthly: report.Monthly,
		SessionsJSON: template.JS(data), SessionCount: len(rows), Truncated: truncated,
	}, nil
}

var statsHTMLTemplate = template.Must(template.New("stats-html").Funcs(template.FuncMap{
	"barHeight":  barHeight,
	"monthShort": monthShort,
}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>deja stats</title><style>
:root{color-scheme:dark;--bg:#0d1117;--panel:#161b22;--line:#30363d;--text:#f0f6fc;--muted:#8b949e;--blue:#58a6ff;--green:#3fb950;--orange:#f78166}*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:14px ui-monospace,SFMono-Regular,Menlo,monospace}main{max-width:1100px;margin:auto;padding:38px 22px}h1{margin:0;color:var(--orange);font-size:28px}p{color:var(--muted)}.range{float:right;color:var(--muted);font-size:13px}.totals{display:grid;grid-template-columns:repeat(3,1fr);gap:12px;margin:32px 0}.stat,.chart,.table-wrap{background:var(--panel);border:1px solid var(--line);border-radius:12px}.stat{padding:18px}.stat b{display:block;font-size:28px}.stat span{color:var(--muted)}h2{font-size:12px;color:var(--muted);letter-spacing:1.5px;margin:30px 0 12px}.chart{height:180px;padding:18px;display:flex;align-items:end;gap:8px}.bar{flex:1;min-width:8px;background:var(--blue);border-radius:4px 4px 0 0;opacity:.8;position:relative}.bar small{position:absolute;top:calc(100% + 8px);left:50%;transform:translateX(-50%);color:var(--muted);font-size:10px}.controls{display:flex;gap:10px;margin:12px 0}.controls input{width:100%;padding:11px;border-radius:8px;border:1px solid var(--line);background:var(--panel);color:var(--text);font:inherit}.table-wrap{overflow:auto}table{width:100%;border-collapse:collapse}th,td{text-align:left;padding:12px;border-bottom:1px solid var(--line);white-space:nowrap}th{color:var(--muted);font-size:11px}td.title{white-space:normal;min-width:260px}.badge{color:var(--green);cursor:pointer}.clickable{cursor:pointer}.empty{padding:20px;color:var(--muted)}footer{color:var(--muted);font-size:12px;margin-top:18px}@media(max-width:650px){main{padding:24px 12px}.range{float:none;display:block;margin-top:10px}.totals{grid-template-columns:1fr}.chart{gap:3px;padding:12px}}
</style></head><body><main><span class="range">{{if .DateStart}}{{.DateStart}} - {{.DateEnd}}{{else}}-{{end}}</span><h1>deja stats</h1><p>indexed agent work, wrapped for sharing</p>
<section class="totals"><div class="stat"><b>{{.TotalSessions}}</b><span>sessions</span></div><div class="stat"><b>{{.TotalMessages}}</b><span>messages</span></div><div class="stat"><b>{{.Harnesses}}</b><span>harnesses</span></div></section>
<h2>ACTIVITY / LAST 12 MONTHS</h2><div class="chart" aria-label="Monthly message activity">{{range .Monthly}}<div class="bar" style="height:{{barHeight .Messages $.Monthly}}%" title="{{.Month}}: {{.Messages}} messages"><small>{{monthShort .Month}}</small></div>{{end}}</div>
<h2>SESSIONS</h2><div class="controls"><input id="filter" type="search" placeholder="Filter sessions by harness, project, or title" aria-label="Filter sessions"></div><div class="table-wrap"><table><thead><tr><th>DATE</th><th>HARNESS</th><th>PROJECT</th><th>TITLE</th><th>MESSAGES</th></tr></thead><tbody id="sessions"></tbody></table><div id="empty" class="empty" hidden>No matching sessions.</div></div>
<footer>{{.SessionCount}} metadata-only sessions embedded. No message text is included in this file.{{if .Truncated}} The embedded list is capped at the 5,000 most recent sessions.{{end}}</footer></main><script>
// Only metadata is embedded below: dates, harnesses, projects, counts, and redacted first-user titles. No message text.
const sessions={{.SessionsJSON}};const tbody=document.getElementById('sessions'),empty=document.getElementById('empty'),input=document.getElementById('filter');
function esc(value){const node=document.createElement('span');node.textContent=value;return node.innerHTML}function render(){const q=input.value.toLowerCase().trim();tbody.innerHTML='';let n=0;sessions.forEach((s,i)=>{const hay=[s.date,s.harness,s.project,s.title].join(' ').toLowerCase();if(q&&!hay.includes(q))return;n++;const row=document.createElement('tr');row.innerHTML='<td>'+esc(s.date)+'</td><td><span class="badge clickable" data-value="'+esc(s.harness)+'">'+esc(s.harness)+'</span></td><td><span class="clickable" data-value="'+esc(s.project)+'">'+esc(s.project)+'</span></td><td class="title">'+esc(s.title||'-')+'</td><td>'+s.messages+'</td>';row.querySelectorAll('.clickable').forEach(e=>e.onclick=()=>{input.value=e.dataset.value;render()});tbody.appendChild(row)});empty.hidden=n!==0}input.oninput=render;render();
</script></body></html>`))

func barHeight(n int, months []stats.MonthStats) int {
	max := 1
	for _, m := range months {
		if m.Messages > max {
			max = m.Messages
		}
	}
	if n == 0 {
		return 4
	}
	return 12 + 88*n/max
}

func monthShort(s string) string {
	if len(s) >= 7 {
		return s[5:]
	}
	return s
}
