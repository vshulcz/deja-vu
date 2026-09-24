<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo-dark.svg">
    <img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo.svg" width="330" alt="deja-vu">
  </picture>
</p>

<p align="center"><b>Ein gemeinsames Gedächtnis für alle Coding-Agents, gebaut aus der Historie, die ohnehin schon auf der Platte liegt.</b></p>

<p align="center">Dein Agent setzt gerade an, etwas neu zu debuggen, das du im März behoben hast — damals in
einem anderen Agenten. deja indiziert die Sessions, die Claude Code, Codex, Cursor und alle übrigen Agents
dieser Maschine ohnehin auf die Platte schreiben, und gibt die richtige an den zurück, der fragt.</p>

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif" width="720" alt="dieselbe Frage zweimal an denselben Agenten: ohne Gedächtnis weiß er nichts, mit deja antwortet er mit einem acht Monate alten Ergebnis"></p>

<p align="center"><sub><em>Niemand hat gesucht — der Agent hat deja von sich aus aufgerufen. Zwei echte Läufe, echtes Modell, echte Tool-Aufrufe, auf einem synthetischen Korpus: es wird niemandes Historie veröffentlicht.</em></sub></p>

<p align="center"><b>deja ist von der ersten Minute an voll: die Historie, die 34 Agents längst geschrieben haben, in Sekunden indiziert, ohne Modell und ohne eigenen Erfassungsschritt.</b></p>

<p align="center">
<b>58 % weniger Tokens</b> bei einer Aufgabe, die diese Maschine schon gelöst hatte &middot; <b>88.1 % hit@1</b> auf LongMemEval-S (bereinigter Satz aus 470 Fragen) &middot; <b>70.5 %</b> auf LoCoMo &middot; Abfragen in <b>Millisekunden</b> über Gigabytes an Historie<br>
<sub>Elf Läufe je Arm: 53,558 Tokens gegen 126,222 ohne angebundenes Gedächtnis, und 71 % in einem späteren Lauf desselben Prüfstands &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/day-zero.html">was eine Aufgabe kostet</a> &middot;
beide Retrieval-Harnesses liegen in diesem Repository und laufen in Minuten auf öffentlichen Datensätzen &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">prüf die Zahlen selbst</a></sub>
</p>

<p align="center"><a href="README.md">English</a> | <a href="README.zh.md">简体中文</a> | <a href="README.zh-TW.md">繁體中文</a> | <a href="README.ja.md">日本語</a> | <a href="README.ko.md">한국어</a> | <a href="README.es.md">Español</a> | <a href="README.pt.md">Português</a> | <a href="README.fr.md">Français</a> | Deutsch | <a href="README.ru.md">Русский</a> | <a href="README.tr.md">Türkçe</a> | <a href="README.hi.md">हिन्दी</a></p>

<p align="center"><a href="https://vshulcz.github.io/deja-vu/">Dokumentation</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">Benchmarks</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/compare.html">Vergleich</a></p>
<p align="center"><sub>Wenn es dir hilft, gib deja-vu einen Stern auf <a href="https://github.com/vshulcz/deja-vu">GitHub</a>.</sub></p>

## Installation

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Falls `raw.githubusercontent.com` nicht erreichbar ist, gibt es einen npm-Spiegel mit derselben Version:

```sh
npm i -g @vshulcz/deja-vu --registry=https://registry.npmmirror.com
deja install --auto
```

Zehn Sekunden zum Installieren, etwa zehn zum Indizieren. Der zweite Befehl bindet MCP-Recall an jeden
gefundenen Agenten, schaltet den Recall beim Session-Start ein, wo der Agent das unterstützt, und baut den
ersten Index, damit die nächste Session auf nichts warten muss.

Öffne eine neue Agenten-Session und frag nach etwas von vor Monaten:

> hatten wir schon mal was mit jwt refresh rotation? schau in deinem Gedächtnis nach

Fragen musst du nicht einmal: mit automatischem Recall weiß der Agent schon beim Öffnen der Session, was in
diesem Projekt gelöst wurde.

opencode, DeepSeek Harness, Zed, Kimi Code, Codex CLI, Grok Build, OpenClaw und pi haben zusätzlich ein Paket
im eigenen Ökosystem, für alle, die Erweiterungen von dort installieren:

```sh
opencode plugin opencode-deja
dsh plugin --profile web add dsh-deja
# Zed: im Erweiterungs-Panel nach deja suchen
# Kimi Code: /plugins install https://github.com/vshulcz/deja-vu
# Codex CLI: codex plugin marketplace add https://github.com/vshulcz/deja-vu && codex plugin add deja-vu@deja-vu
# Grok Build: grok plugin marketplace add xai-org/plugin-marketplace && grok plugin install deja
openclaw plugins install clawhub:@vshulcz/openclaw-deja
pi install npm:@vshulcz/pi-deja
```

`deja install --auto` bindet all das ohnehin an, einer der beiden Wege genügt also. Beide zusammen stören auch
nicht: jedes Paket liest, was `deja install` geschrieben hat, und ergänzt nur das Fehlende — keine doppelt
registrierten Tools, kein zweiter Recall. Die Details stehen in [`extensions/`](extensions).

Dieselbe Suche gibt es als Skill, installierbar von jedem Agenten, der `SKILL.md` liest:

```sh
npx skills add https://github.com/vshulcz/deja-vu --skill deja-search   # skills CLI: Claude Code, Cursor, Goose, Copilot…
openclaw skills install @vshulcz/deja-search                            # ClawHub
hermes skills install vshulcz/deja-vu/skills/deja-search                # Hermes
```

Der Skill ruft das installierte `deja`-Binary auf und bringt kein eigenes mit.

Weitere Wege: `brew install deja-vu`,
`go install github.com/vshulcz/deja-vu/cmd/deja@latest`, oder `npx @vshulcz/deja-vu "suchbegriff"`, um es ohne
Installation auszuprobieren. Unter Windows bricht das Installationsskript mit `unsupported OS` ab, weil es ein
Shell-Skript ist. Nimm `deja-vu_<version>_windows_amd64.zip` aus dem
[letzten Release](https://github.com/vshulcz/deja-vu/releases/latest) und leg `deja.exe` in den `PATH`.

Das Binary allein ist bereits eine vollständige Installation: Indizieren, Suchen, `show`, `ctx`, `blame`,
`--json` und das Entfernen von Zugangsdaten brauchen nichts weiter. Was `deja install` macht, ist MCP an deine
Agents anzubinden und den Recall beim Session-Start einzuschalten — sinnvoll, aber optional.

## Was du davon hast

**In Codex gelöst, von Claude erinnert.** Vierunddreißig Coding-Agents schreiben jedes Gespräch in lokale
Dateien, und deja macht aus diesen Dateien eine Gedächtnisschicht, die sie alle lesen können.

| | |
| --- | --- |
| **Suche rückwärts in der Zeit** | `deja "connection pool exhausted"` durchsucht Gigabytes, auch alles von vor der Installation von deja. Eine Frage in natürlicher Sprache fällt auf den Relevanzmodus zurück. Zeit ist ein Hinweis, kein Filter. |
| **Recall über Agenten hinweg** | Das MCP-Tool `deja` im Modus `recall` antwortet aus jedem Agenten heraus „das haben wir vor drei Wochen behoben“, egal wer es damals behoben hat. |
| **Übersteht die Kompaktierung** | Gemessen an 43 Kompaktierungen: die Zusammenfassung behielt 77 % der Entscheidungen und 0.2 % der Befehle, die du ausgeführt hast. Die übrigen 99.8 % gibt deja zurück. In Claude Code und Codex notiert deja Aufgabe, Dateien und Befehle in dem Moment, in dem die Kompaktierung beginnt, und gibt sie in der nächsten Session am Stück zurück. |
| **Recall im Moment des Handelns** | Bevor der Agent eine Datei ändert oder einen Befehl ausführt, nennt der `PreToolUse`-Hook die früheren Entscheidungen zu dieser Datei, die hier funktionierende Form dieses Befehls, oder dass das Programm auf dieser Maschine schlicht nicht existiert. Scheitert ein Befehl, zeigt der `PostToolUse`-Hook, was auf dieser Maschine nach demselben Fehler ausgeführt wurde — genau das Paar, nach dem der Agent nicht fragt. |
| **Indiziert die Arbeit, nicht nur das Gesagte** | Jede in einem Zug geöffnete Datei, jeder ausgeführte Befehl samt Exit-Code und das exakte Fragment, das eine Änderung ersetzt hat. Genau das, was jede Zusammenfassung verliert. |

Außerdem: `deja promote <id> --state rejected` markiert eine verworfene Entscheidung, und ab dann zeigt jeder
Treffer, dass sie versucht und abgelehnt wurde; ein Treffer meldet „4 Dateien dieser Session haben sich seitdem
geändert“ und schweigt, wenn er es nicht beurteilen kann; `deja sync ssh laptop` bewegt das Gedächtnis zwischen
Maschinen rein additiv, ohne Cloud dazwischen; `deja handoff --to codex` packt den aktuellen Kontext, um in
einem anderen Agenten weiterzumachen; Schlüssel, Tokens, JWTs und private Schlüsselblöcke bekannter Form werden
beim Indizieren entfernt — Mustererkennung ist allerdings keine Secret-Erkennung, und eine unbekannte Form kann
durchrutschen.

### Zeichne deine eigene Arbeit

`deja stats --card` zeichnet direkt im Terminal; gib ihm einen Dateinamen, und es schreibt ein SVG für das
README deines Profils. Zum Veröffentlichen anderswo
[wandle es in PNG um](https://vshulcz.github.io/deja-vu/card/) — diese Seite konvertiert in deinem eigenen
Browser.

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/docs/assets/stats-card-demo.svg" width="760" alt="deja-Statistikkarte: ein Jahr Sessions als Heatmap, aus welchen Agenten sie stammen und welche die längste war"></p>

Die vollständige Referenz steht auf der [Dokumentationsseite](https://vshulcz.github.io/deja-vu/).

## Privatsphäre

Indizieren und Suchen laufen lokal. Das Netz brauchen nur `deja update`, `deja sync ssh`, die Versionsprüfung
in `deja doctor` und `deja embed`, das an den von dir konfigurierten Endpunkt geht.

Zugangsdaten werden beim Indizieren entfernt: AWS-Schlüssel, Zuweisungen mit `api_key=` und `token=`,
Bearer-Tokens und nackte JWTs, PEM-Blöcke privater Schlüssel, Tokens verschiedener Anbieter, URLs der Form
`scheme://user:pass@host`, hochentropische Werte, die keine Regel abdeckt, und Passwörter im Fließtext — „the
admin password is …“, wo es kein Trennzeichen gibt, an dem man sich festhalten könnte. Der Wert wird zu
`[redacted:<kind>]`, der Text ringsum bleibt durchsuchbar. `deja share` und `deja sync export` säubern beim
Export ein zweites Mal. Mustererkennung ist keine Secret-Erkennung: eine Form, die die Regeln nicht kennen,
kann unverändert durchgehen — siehe Sicherheitsmodell.

`deja forget` nimmt Sessions aus dem neu gebauten Index und hinterlässt einen Grabstein, sodass ein
anschließendes `deja index` sie nicht aus der ursprünglichen Historie zurückholen kann.
Das [Sicherheitsmodell](docs/SECURITY-MODEL.md) dokumentiert die Datenflüsse, die Grenzen der Bereinigung, die
Vertrauensannahmen und die Release-Verifikation.

## Kommandozeile

```text
$ deja "jwt refresh token"
[claude] api        · Jul 8 · 8f31c0a9 — 2 matches
  login started failing after refresh token rotation; jwt kid mismatch in tests
  fixed by reloading jwks cache after rotateKey and adding a clock-skew test
[codex]  web        · Jul 1 · b77d91e2 — 1 match
  refresh token cookie needed SameSite=Lax in local callback flow
```

| Befehl | Was er tut |
| --- | --- |
| `deja <suchbegriff>` | Durchsucht die gesamte Historie. Mehrere Wörter sind UND, Anführungszeichen verlangen zusammenhängenden Text; ohne exakten Treffer werden Wortformen und ähnliche Schreibweisen probiert. |
| `deja wip` | Woran die letzte Session in diesem Verzeichnis gearbeitet hat: die Aufgabe, was entschieden wurde, die offenen Dateien, der letzte Befehl und ob er fehlschlug. Alles aus den Aufzeichnungen abgeleitet, ohne auf Notizen angewiesen zu sein. |
| `deja blame <pfad>[:zeile]` | Welche Sessions diese Datei besprochen haben, was entschieden wurde und warum. Mit Zeilennummer: der Commit, der sie zuletzt geändert hat, und die Session, die den von diesem Commit gelöschten Text geschrieben hat. |
| `deja files <thema>` | Die Gegenrichtung: welche Dateien die Arbeit an einem Thema tatsächlich angefasst hat. |
| `deja how <werkzeug>` | Wie das auf dieser Maschine wirklich ausgeführt wird, mit echten Argumenten, aus Befehlen, die Agents bereits ausgeführt haben. |
| `deja fix <fehler>` | Was auf dieser Maschine nach demselben Fehler ausgeführt wurde und wonach der Fehler nicht wieder auftrat. |
| `deja friction` | Fehler, die in mehr als drei verschiedenen Sessions auftauchen, mit Angabe der Werkzeuge. |
| `deja ctx <suchbegriff>` | Markdown-Zusammenfassung der besten Treffer, direkt in einen Prompt einsetzbar. |
| `deja resume <id>` | Öffnet die gefundene Session wieder in dem Werkzeug, zu dem sie gehört. |
| `deja view` | Exportiert das gesamte Gedächtnis in eine einzelne lokale HTML-Datei. Kein Server, nichts verlässt die Maschine. |
| `deja doctor [--deep]` | Selbstprüfung; mit `--deep` wird der Index gegen die Quelldateien verifiziert. |
| `deja mcp` | Der MCP-Server über stdio — genau der, den `deja install` anbindet. |

Die vollständige Referenz steht in der [Befehlsdokumentation](https://vshulcz.github.io/deja-vu/guide/commands.html).

### MCP-Tools

Der Server stellt ein einziges Tool `deja` bereit, der Parameter `mode` wählt die Fähigkeit: `recall`,
`context`, `blame`, `fix`, `how`, `remember`. `deja install` bindet es selbst an, das ist also nur bei
manueller Konfiguration eines Agenten relevant. Die früheren sechs Tool-Namen funktionieren bei bereits
angebundenen Clients weiter.

Ein Tool statt sieben ist eine Kostenfrage, keine Stilfrage. Ein angebundener MCP-Server schickt die
Definitionen seiner Tools mit jeder Anfrage mit, sie werden also in jedem Zug bezahlt, ob der Agent etwas
aufgerufen hat oder nicht: 477 Tokens hier, gegenüber 8,283 beim größten der acht gemessenen Server. dejas
eigener Wert lag bei 828, bis das Schema auf ein Tool mit Modi reduziert wurde.

## Unterstützte Werkzeuge

Mit automatischem Recall übergeben Claude Code und Codex deja die laufende Aufzeichnung, sobald die
Kompaktierung beginnt, und deja behält, was die Zusammenfassung gleich verlieren wird: die Aufgabe, die
Ergebnisse, die Dateien, was aus jedem Befehl wurde, und was noch offen ist. Der nächste Hook in derselben
Session und demselben Arbeitsverzeichnis gibt das am Stück zurück, nicht mehr als 4 KB, mit einer Zeile dazu,
ob sich das Repository seitdem geändert hat. `deja stats` zählt die Tool-Aufrufe zwischen Kompaktierung und
erster Änderung — damit wurde das gemessen.
Die Details stehen in [Wiederherstellung nach der Kompaktierung](docs/compaction.md).

Claude Code · Cline · Codex CLI · opencode · aider · Gemini CLI · Cursor · Antigravity ·
Grok Build · Hermes · Goose · Qwen Code · Kimi Code · pi · omp (Oh My Pi) · OpenClaw ·
Copilot CLI · VS Code Copilot Chat · Amp · prime-agent (PrimeIntellect) · Roo Code ·
Continue · Crush · DeepSeek Harness · Cherry Studio · Senpi · gajae-code · Kimchi Coding ·
Command Code · ZCode · CodeWhale · Kiro · Kilo Code · Zed.

Was jedes einzelne unterstützt — MCP-Recall, automatischer Recall, Skills, Befehle, Resume, Handoff — steht in
der [Fähigkeitsmatrix im englischen README](README.md#supported-harnesses). Abweichende Speicherorte werden
über `DEJA_*_ROOT`-Variablen angegeben, und die Migrationsvariablen der Werkzeuge selbst werden ebenfalls
beachtet.

### Agents mit eigenem Paket

`deja install --auto` bindet diese genauso an wie alle anderen, und das ist immer der kürzeste Weg. Zusätzlich
hat jeder ein Paket im eigenen Ökosystem, praktisch für alle, die Erweiterungen von dort installieren:

| Agent | Paket | Installation |
| --- | --- | --- |
| opencode | npm `opencode-deja` | `opencode plugin opencode-deja` |
| DeepSeek Harness | npm `dsh-deja` | `dsh plugin --profile web add dsh-deja` |
| Zed | `deja-context-server` | Zed → Extensions → deja |
| Kimi Code | Plugin `deja` | `/plugins install https://github.com/vshulcz/deja-vu` |
| Codex CLI | Plugin `deja-vu` | `codex plugin marketplace add https://github.com/vshulcz/deja-vu`, dann `codex plugin add deja-vu@deja-vu` |
| Grok Build | Plugin `deja` | `grok plugin marketplace add xai-org/plugin-marketplace`, dann `grok plugin install deja` |
| OpenClaw | ClawHub und npm `@vshulcz/openclaw-deja` | `openclaw plugins install clawhub:@vshulcz/openclaw-deja` |
| pi (und omp) | npm `@vshulcz/pi-deja` | `pi install npm:@vshulcz/pi-deja` |

Jeder der beiden Wege reicht, beide zusammen gehen auch: jedes Paket liest zuerst, was `deja install`
hinterlassen hat. opencode, dsh und OpenClaw ergänzen nur das Fehlende; Kimi, Grok, Codex und pi treten zurück,
wenn der Installer schon angebunden hat; in Zed benutzen beide Seiten dieselbe Server-ID. Die Reihenfolge der
Installation spielt also keine Rolle.

Alle benutzen das deja, das du bereits installiert hast; die Kopie im Paket ist nur der Rückfall.

## Semantischer Recall, optional

Richte `deja embed` über `DEJA_EMBED_URL` auf ein lokales Ollama, LM Studio oder einen beliebigen
OpenAI-kompatiblen Endpunkt, dann trifft auch eine anders formulierte Frage. Ohne verfügbare Runtime
funktionieren lexikalische Suche und MCP-Recall wie gewohnt.

## Belege

```sh
deja bench recall     # Untergrenze für Ranking-Regression: 100 Abfragen, die Hälfte auf Russisch, CI schlägt fehl, wenn der Recall sinkt
deja bench context    # 30 Aufgabenketten mit Seed plus fünf Negativkontrollen
deja bench block      # ob die Antwort im übergebenen Textstück noch enthalten ist
deja bench prompt     # wann der Prompt-Hook spricht und wann er falsch spricht
deja bench ingest     # Kosten einer Indexaktualisierung: nichts geändert, ein Zug angehängt, eine neue Aufzeichnungsdatei, vollständiges Neuschreiben
```

Das Kontextexperiment vergleicht dejas Recall mit vollständiger Historie, naivem grep und Kaltstart. Mit dem
Standard-Seed:

| Ansatz | Tokens (Median) | Abdeckung (Median) | Tokens der Negativkontrolle |
| --- | ---: | ---: | ---: |
| deja-recall | 1,096 | 1.00 | 0 |
| full-history | 80,547 | 1.00 | 78,145 |
| naive-grep | 273,238 | 1.00 | 0 |
| cold | 0 | 0.00 | 0 |

Dieselbe Faktenabdeckung wie grep über die Rohlogs bei etwa 250-mal weniger Tokens; rund 70-mal weniger als bei
Sessions, die durch vollständiges Abspielen gefunden werden; und nichts injiziert in Ketten ohne passende
Historie. Der Korpusgenerator und die Relevanzannotation sind gewöhnlicher, lesbarer Go-Code. Bevor du
irgendeiner Zahl glaubst, sieh nach, wie sie „relevant“ definiert — auch bei unseren.

Gemessen an einem echten Repository: 2,419 Sessions, 179k Nachrichten, 1.9 GB an Aufzeichnungen.

| Kennzahl | Ergebnis |
| --- | --- |
| Abfrage im Prozess | Median **0.7–0.8 ms**, etwa 19 ms auf den Heuhaufen von LongMemEval-S |
| `deja <suchbegriff>` Ende zu Ende | Median etwa 0.2 s auf diesem Repository: Prozessstart, Aktualitätsprüfung aller Speicher, Ranking, Ausgabe |
| Nur die Aktualitätsprüfung | etwa 50 ms, wenn sich nichts geändert hat |
| Indexgröße | 200 MB, rund 10 % des Korpus |

Der Index ist inkrementell. Wächst eine Session-Datei, wird nur diese eine Datei neu gelesen.

## Wie es funktioniert

Ein lokaler invertierter Index in `~/.cache/deja`: er parst JSONL- und SQLite-Speicher, entfernt Zugangsdaten,
schreibt `records.bin` und Wort-Buckets und hält den Zustand jeder Datei in `manifest.gob`, sodass ein zweiter
Lauf nur das Geänderte aufnimmt. MCP-Server, Statistiken, Share und Sync lesen denselben Index. Die Details
stehen in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Häufige Fragen

**Verlässt irgendetwas meine Maschine?** Nein, außer du verlangst es. Siehe
[Datenflüsse](docs/SECURITY-MODEL.md#data-flows).

**Und die Secrets, die schon in den Logs stehen?** Die bleiben in den Dateien des jeweiligen Werkzeugs, das
sind die Daten deines Agenten. In dejas Index, in Zusammenfassungen, in Share und in den Sync-Export kommen sie
nicht.

**Bremst das meinen Agenten?** Ein Recall ist eine lexikalische Abfrage auf einen lokalen Index: Median
0.7–0.8 ms, und nichts wartet auf ein Modell. Der Hook fügt Prozessstart und Aktualitätsprüfung der Speicher
hinzu — Zehntelsekunden im Bereich einiger Dutzend Millisekunden bei einem mehrere Gigabyte großen Repository.

**Muss ich meine Arbeitsweise ändern?** Nein. Den Recall ruft der Agent selbst auf; mit automatischem Recall
weiß er schon beim Öffnen der Session, was in diesem Projekt zuvor entschieden wurde.

**Wie unterscheidet sich das von anderen Gedächtnis-Werkzeugen?**

| | deja | Gedächtnis-Plattformen<br>(Mem0, Letta, memU) | Session-Suche<br>(cass) |
| --- | :-: | :-: | :-: |
| Kennt die Arbeit vor der eigenen Installation | ja | nein | ja |
| Braucht einen Erfassungsschritt | nein, das Transkript ist das Gedächtnis | Agent oder dein Code schreiben Fakten | nein |
| Braucht einen LLM- oder Embedding-Schlüssel | nein | ja | optional |
| Ruft ab, ohne gefragt zu werden | beim Session-Start und vor der Tool-Ausführung | nein | nein |

Der [vollständige Vergleich](https://vshulcz.github.io/deja-vu/guide/compare.html) deckt elf davon ab.

**Wo liegt die Session-Historie von Claude Code, und kann man sie durchsuchen?** Unter `~/.claude/projects`,
eine JSONL-Datei je Session; Codex unter `~/.codex/sessions`, Cursor in der SQLite-Datei `state.vscdb`.
`deja search` liest sie an Ort und Stelle, `deja last` listet die jüngste Session jedes Agenten, und
`deja view` öffnet die gesamte Historie als lokale Seite. Die Pfade je Agent stehen unter
[wo Sessions gespeichert werden](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html).

**Meine Claude-Code-Historie ist verschwunden, ist sie weg?** Claude Code löscht Aufzeichnungen, die älter als
30 Tage sind (`cleanupPeriodDays` in `~/.claude/settings.json`), und `claude --resume` listet nur, was übrig
ist. Sessions, die deja vor dem Aufräumen indiziert hat, bleiben durchsuchbar, auch nachdem die Datei
verschwunden ist. Die Details stehen unter
[Session-Dateien auf der Platte](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html).

**Wie lösche ich alles?**

```sh
deja uninstall --all
rm -rf ~/.cache/deja
```

## Leitfäden

Nach Situationen geschrieben, nicht nach Funktionen:

- [Erinnert sich ein Coding-Agent an frühere Gespräche?](https://vshulcz.github.io/deja-vu/guide/does-my-agent-remember.html) — was jeder Agent zwischen Sessions behält und was er verliert
- [Session-Dateien auf der Platte](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html) — wie groß `~/.claude/projects` wird und was Löschen kostet
- [Der Agent hat gerade deinen Kontext verloren](https://vshulcz.github.io/deja-vu/guide/lost-context.html) — nach einem Absturz, nach einem Clear, oder wenn die Session leer zurückkommt
- [Das Kontextfenster ist voll](https://vshulcz.github.io/deja-vu/guide/context-window-full.html) — was die Kompaktierung wirklich behält, mit Messungen, und was man stattdessen tun kann
- [Die Session von gestern fortsetzen](https://vshulcz.github.io/deja-vu/guide/resume-a-session.html) — sie über alle Agents hinweg finden und in ihrem eigenen wieder öffnen
- [Der Agent hat einen Fehler wiederholt, den du schon behoben hattest](https://vshulcz.github.io/deja-vu/guide/repeated-mistakes.html)
- [Die Session finden, in der es gelöst wurde](https://vshulcz.github.io/deja-vu/guide/find-a-session.html)
- [Warum Agents zwischen Sessions vergessen](https://vshulcz.github.io/deja-vu/guide/forgetting.html) · [wo jeder Agent seine Historie ablegt](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html)
- [Was die Kompaktierung verliert](https://vshulcz.github.io/deja-vu/guide/after-compaction.html) · [den Agenten wechseln](https://vshulcz.github.io/deja-vu/guide/switching-agents.html) · [prüfen, was Agents getan haben](https://vshulcz.github.io/deja-vu/guide/auditing-agents.html) · [ein Gespräch exportieren](https://vshulcz.github.io/deja-vu/guide/export-conversations.html) · [zwischen Maschinen](https://vshulcz.github.io/deja-vu/guide/sync-across-machines.html) · [was Gedächtnis an Tokens kostet](https://vshulcz.github.io/deja-vu/guide/token-cost.html)

Nach Werkzeug: [opencode](https://vshulcz.github.io/deja-vu/guide/memory-for-opencode.html) · [Zed](https://vshulcz.github.io/deja-vu/guide/memory-for-zed.html) · [Grok Build](https://vshulcz.github.io/deja-vu/guide/memory-for-grok.html) · [Gemini CLI](https://vshulcz.github.io/deja-vu/guide/memory-for-gemini.html) · [OpenClaw](https://vshulcz.github.io/deja-vu/guide/memory-for-openclaw.html) · [Goose](https://vshulcz.github.io/deja-vu/guide/memory-for-goose.html) · [Cline](https://vshulcz.github.io/deja-vu/guide/memory-for-cline.html) · [pi and omp](https://vshulcz.github.io/deja-vu/guide/memory-for-pi.html) · [Hermes](https://vshulcz.github.io/deja-vu/guide/memory-for-hermes.html)

## Probier es an deiner eigenen Historie

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Zehn Sekunden installieren, etwa zehn indizieren. Wenn ein Agent das nächste Mal eine Session öffnet, weiß er
bereits, was du in diesem Projekt gelöst hast — auch das von vor der Installation von deja.

## Entwicklung

`make build test lint`, danach [CONTRIBUTING.md](CONTRIBUTING.md).
Ein neues Werkzeug beginnt bei der [Parser-Registry](docs/ARCHITECTURE.md#source-parsers).
Prioritäten und Nicht-Ziele stehen in [ROADMAP.md](ROADMAP.md).

## Lizenz

MIT © [Vladislav Shulcz](https://github.com/vshulcz)
