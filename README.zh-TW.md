<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo-dark.svg">
    <img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo.svg" width="330" alt="deja-vu">
  </picture>
</p>

<p align="center"><b>所有編碼代理共用的一份記憶，來自你磁碟上已有的歷史。</b></p>

<p align="center">你的代理正準備重新除錯一個你三月就修好的問題——當時是在另一個代理裡修的。deja 索引 Claude Code、Codex、Cursor
以及這台機器上其他所有代理本來就寫在磁碟上的會話，無論哪個代理來問，都把對的那一筆交回去。</p>

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif" width="720" alt="同一個問題問同一個代理兩次：沒有記憶時牠毫無印象，有 deja 時牠用八個月前的結論作答"></p>

<p align="center"><sub><em>沒有人去搜尋——是代理自己呼叫了 deja。兩次真實執行，真實模型、真實工具呼叫，跑在合成語料上：不會公開任何人的歷史。</em></sub></p>

<p align="center"><b>deja 一開始就是滿的：34 個代理早已寫下的歷史，幾秒建好索引，不需要模型，也不需要額外的蒐集步驟。</b></p>

<p align="center">
同一台機器已經做過的任務，<b>少花 58% 的 token</b> &middot; LongMemEval-S（470 題清理集）上 <b>88.1% hit@1</b> &middot; LoCoMo 上 <b>70.5%</b> &middot; 數 GB 歷史上的查詢在<b>毫秒</b>級<br>
<sub>每組 11 次執行：53,558 token，對比未接上任何記憶時的 126,222；同一套測試架的後續一次執行為 71% &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/day-zero.html">完成一個任務要花多少</a> &middot;
兩套檢索評測都在本儲存庫裡，幾分鐘即可在公開資料集上跑完 &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">自己核對這些數字</a></sub>
</p>

<p align="center"><a href="README.md">English</a> | <a href="README.zh.md">简体中文</a> | 繁體中文 | <a href="README.ja.md">日本語</a> | <a href="README.ko.md">한국어</a> | <a href="README.es.md">Español</a> | <a href="README.pt.md">Português</a> | <a href="README.fr.md">Français</a> | <a href="README.de.md">Deutsch</a> | <a href="README.ru.md">Русский</a> | <a href="README.tr.md">Türkçe</a> | <a href="README.hi.md">हिन्दी</a></p>

<p align="center"><a href="https://vshulcz.github.io/deja-vu/">文件</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">評測</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/compare.html">與同類比較</a></p>
<p align="center"><sub>覺得有用的話，歡迎在 <a href="https://github.com/vshulcz/deja-vu">GitHub</a> 上給 deja-vu 點個 Star。</sub></p>

## 安裝

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

連不上 `raw.githubusercontent.com` 時走 npm 鏡像，版本是同一個：

```sh
npm i -g @vshulcz/deja-vu --registry=https://registry.npmmirror.com
deja install --auto
```

安裝十秒，建索引約十秒，然後就能用了。第二條指令會把 MCP 召回接到它找到的每一個代理上，
在支援的地方開啟會話啟動時的召回，並建好第一份索引，這樣下一次會話不必再等。

開一個新的代理會話，問一件幾個月前做過的事：

> 我們以前處理過 jwt refresh rotation 嗎？查一下你的記憶

也不必特意去問——開啟自動召回後，會話一打開，代理就已經知道你在這個專案裡解決過什麼。

opencode、DeepSeek Harness、Zed、Kimi Code、Codex CLI、Grok Build、OpenClaw 和 pi 也有各自生態裡的套件，習慣在那邊裝擴充的人
可以直接用：

```sh
opencode plugin opencode-deja
dsh plugin --profile web add dsh-deja
# Zed：擴充面板裡搜 deja
# Kimi Code：/plugins install https://github.com/vshulcz/deja-vu
# Codex CLI：codex plugin marketplace add https://github.com/vshulcz/deja-vu && codex plugin add deja-vu@deja-vu
# Grok Build：grok plugin marketplace add xai-org/plugin-marketplace && grok plugin install deja
openclaw plugins install clawhub:@vshulcz/openclaw-deja
pi install npm:@vshulcz/pi-deja
```

`deja install --auto` 已經把上面這些都接好了，兩條路走哪條都夠。兩邊都裝也沒問題：套件會看
`deja install` 寫了什麼，只補上缺的部分，不會重複註冊工具、也不會重複召回。詳見
[`extensions/`](extensions)。

同一套搜尋也是一個 skill，任何會讀 `SKILL.md` 的代理都能裝：

```sh
npx skills add https://github.com/vshulcz/deja-vu --skill deja-search   # skills CLI：Claude Code、Cursor、Goose、Copilot…
openclaw skills install @vshulcz/deja-search                            # ClawHub
hermes skills install vshulcz/deja-vu/skills/deja-search                # Hermes
```

skill 呼叫的是上面裝好的 `deja` 執行檔，自己不帶。

其他安裝方式：`brew install deja-vu`、
`go install github.com/vshulcz/deja-vu/cmd/deja@latest`，或者用
`npx @vshulcz/deja-vu "查詢詞"` 先試試而不裝任何東西。Windows 上安裝腳本會結束並提示
`unsupported OS`——它是 shell 腳本，請從
[最新發行版](https://github.com/vshulcz/deja-vu/releases/latest)取
`deja-vu_<version>_windows_amd64.zip`，把 `deja.exe` 放進 `PATH`。

只有執行檔也是一次完整安裝：索引、搜尋、`show`、`ctx`、`blame`、`--json`
和遮蔽都不需要別的東西。`deja install` 負責的是把 MCP 接進你的代理、開啟會話啟動召回——值得有，但可選。

## 能得到什麼

**在 Codex 裡解決，Claude 記得。** 三十四個編碼代理把每一次對話都寫進本機檔案，
deja 把這些檔案變成一層牠們都能讀的記憶。

| | |
| --- | --- |
| **回溯式搜尋** | `deja "connection pool exhausted"` 搜遍數 GB，包括你安裝 deja 之前的一切。自然語言提問會退回到相關性檔位。時間是提示，不是過濾條件。 |
| **跨代理召回** | MCP 的 `deja` 工具用 `recall` 模式在任何一個代理裡都能回答「這個我們三週前修過」，不管當初是誰修的。 |
| **壓縮之後仍然在** | 在 43 次上下文壓縮上實測：摘要保住了 77% 的決策和 0.2% 的你跑過的指令。其餘 99.8% 由 deja 交回。在 Claude Code 和 Codex 上，壓縮剛開始時 deja 就把任務、檔案和指令記下來，下一個會話裡一次交回。 |
| **在動手的那一刻召回** | 代理改檔案或跑指令之前，`PreToolUse` 掛鉤會說出這個檔案此前的決定、這條指令能用的寫法，或者這台機器上根本沒有的那個程式。指令失敗時，`PostToolUse` 掛鉤給出這台機器上同樣報錯之後跑過什麼——那正是代理不會主動去問的一對。 |
| **索引的是活兒，不只是話** | 每一輪打開過的檔案、跑過的指令及其結束碼、以及一次編輯替換掉的確切片段。那正是所有摘要都會丟掉的部分。 |

還有：`deja promote <id> --state rejected` 標註被推翻的決定，此後每一次命中都會顯示它試過並被否決；
命中會回報「本會話涉及的 4 個檔案此後已變更」，判斷不了時就不說；
`deja sync ssh laptop` 在機器之間搬運記憶，只追加、中間沒有雲端；
`deja handoff --to codex` 把當前上下文打包，好在另一個代理裡接著做；
已知形態的金鑰、token、JWT 和私鑰區塊在建索引時會被剝掉；樣式比對不是金鑰偵測，未知形態可能漏過。

### 把你自己的工作畫出來

`deja stats --card` 直接畫在終端機裡；給它一個檔名，它會寫出一張 SVG，可以放進個人主頁的 README。要發到別處，就把它[轉成 PNG](https://vshulcz.github.io/deja-vu/card/)——那個頁面在你自己的瀏覽器裡完成轉換。

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/docs/assets/stats-card-demo.svg" width="760" alt="deja 統計卡片：一年的會話熱力圖、牠們來自哪些代理、以及最長的一次"></p>

完整的功能參考在[文件站](https://vshulcz.github.io/deja-vu/)。

## 隱私

建索引和搜尋都在本機。只有 `deja update`、`deja sync ssh`、`deja doctor` 裡的版本檢查，以及指向你所設定端點的 `deja embed` 會用到網路。

憑證在建索引時遮蔽：AWS 金鑰、`api_key=` 與 `token=` 賦值、bearer token 與裸 JWT、PEM 私鑰區塊、
各家供應商的 token、`scheme://user:pass@host` 形式的 URL、沒有規則能比對的高熵值，
以及寫在散文裡的密碼——「the admin password is …」這種沒有任何分隔符可依的寫法。
值會變成 `[redacted:<kind>]`，周圍的文字仍可搜尋。`deja share` 和 `deja sync export` 在匯出時再做一次遮蔽。樣式比對不是金鑰偵測：規則不認識的形態可能原樣通過，見安全模型。

`deja forget` 把會話從重建後的索引裡移除並寫下墓碑，之後的 `deja index` 無法從原始歷史裡把牠們還原回來。
[安全模型](docs/SECURITY-MODEL.md)記錄了資料流向、遮蔽的邊界、信任假設與發行版驗證。

## 命令列

```text
$ deja "jwt refresh token"
[claude] api        · Jul 8 · 8f31c0a9 — 2 matches
  login started failing after refresh token rotation; jwt kid mismatch in tests
  fixed by reloading jwks cache after rotateKey and adding a clock-skew test
[codex]  web        · Jul 1 · b77d91e2 — 1 match
  refresh token cookie needed SameSite=Lax in local callback flow
```

| 指令 | 作用 |
| --- | --- |
| `deja <查詢詞>` | 搜尋所有歷史。多個詞是 AND，引號內要求連續文字；沒有精確命中時會嘗試詞形與近似拼寫。 |
| `deja wip` | 這個目錄裡上一個會話在做什麼：任務、定下來的結論、手上的檔案、最後一條指令以及它是否失敗。全部從記錄裡推出來，不依賴誰記得寫筆記。 |
| `deja blame <路徑>[:行號]` | 哪些會話討論過這個檔案、當時決定了什麼、為什麼。給出行號時：最後改動這一行的 commit，以及寫下被這次 commit 刪掉的那段文字的會話。 |
| `deja files <主題>` | 反方向：某個主題的工作實際動過哪些檔案。 |
| `deja how <工具>` | 這台機器實際怎麼跑一件事，帶真實參數，來自代理此前跑過的指令。 |
| `deja fix <報錯>` | 這台機器上同樣的報錯之後跑過什麼，且那次之後錯誤沒有再出現。 |
| `deja friction` | 命中三個以上不同會話的報錯，並指出來自哪些工具。 |
| `deja ctx <查詢詞>` | 最佳命中的 Markdown 摘要，可直接接進提示詞。 |
| `deja resume <id>` | 在原來的工具裡重新開啟找到的那個會話。 |
| `deja view` | 把整個記憶匯出成一個本機 HTML 檔。沒有伺服器，資料不離開本機。 |
| `deja doctor [--deep]` | 自我檢查；加 `--deep` 時用來源檔案驗證索引。 |
| `deja mcp` | stdio 的 MCP 伺服器，也就是 `deja install` 接進去的那個。 |

完整參考見[指令文件](https://vshulcz.github.io/deja-vu/guide/commands.html)。

### MCP 工具

伺服器只暴露一個工具 `deja`，用 `mode` 參數選擇能力：`recall`、`context`、`blame`、`fix`、`how`、`remember`。
`deja install` 會自動接好，只有手動設定代理時才需要在意它。原來的六個工具名對已經接好的客戶端仍然有效。

一個工具而不是七個，是成本問題，不是風格問題。接上的 MCP 伺服器會把工具定義隨每一次請求一起送出，
所以無論代理有沒有呼叫，每一輪都在付這筆錢：這裡是 477 token，實測的八個伺服器裡最大的那個是 8,283。
deja 自己在把結構縮成一個帶模式的工具之前，也是 828。

## 支援的工具

開啟自動召回後，Claude Code 和 Codex 會在壓縮開始時把當前記錄交給 deja，牠留下摘要即將丟掉的東西：
任務、結論、檔案、每條指令跑成了什麼、以及還剩什麼沒做完。同一個會話、同一個工作目錄下的下一個掛鉤
會把這些一次交回，總量不超過 4 KB，並附一行說明儲存庫此後有沒有變動。
`deja stats` 統計壓縮之後到第一次編輯之間的工具呼叫次數，這個功能就是拿它來衡量的。
細節見[壓縮後自動復原](docs/compaction.md)。

Claude Code · Cline · Codex CLI · opencode · aider · Gemini CLI · Cursor · Antigravity ·
Grok Build · Hermes · Goose · Qwen Code · Kimi Code · pi · omp (Oh My Pi) · OpenClaw ·
Copilot CLI · VS Code Copilot Chat · Amp · prime-agent (PrimeIntellect) · Roo Code ·
Continue · Crush · DeepSeek Harness · Cherry Studio · Senpi · gajae-code · Kimchi Coding ·
Command Code · ZCode · CodeWhale · Kiro · Kilo Code · Zed。

每個工具分別支援 MCP 召回、自動召回、skill、指令、resume 和 handoff 中的哪些，見
[英文 README 的能力矩陣](README.md#supported-harnesses)。自訂儲存位置透過 `DEJA_*_ROOT`
變數指定，各家自己的遷移變數也會被尊重。

### 自帶套件的代理

`deja install --auto` 會像接其他工具一樣把下面這幾個接好，那始終是最短的一條路。
牠們同時在各自的生態裡有一個套件，方便習慣從那邊安裝擴充的人：

| 代理 | 套件 | 安裝 |
| --- | --- | --- |
| opencode | npm `opencode-deja` | `opencode plugin opencode-deja` |
| DeepSeek Harness | npm `dsh-deja` | `dsh plugin --profile web add dsh-deja` |
| Zed | `deja-context-server` | Zed → Extensions → deja |
| Kimi Code | 外掛 `deja` | `/plugins install https://github.com/vshulcz/deja-vu` |
| Codex CLI | 外掛 `deja-vu` | `codex plugin marketplace add https://github.com/vshulcz/deja-vu`，然後 `codex plugin add deja-vu@deja-vu` |
| Grok Build | 外掛 `deja` | `grok plugin marketplace add xai-org/plugin-marketplace`，然後 `grok plugin install deja` |
| OpenClaw | ClawHub 與 npm `@vshulcz/openclaw-deja` | `openclaw plugins install clawhub:@vshulcz/openclaw-deja` |
| pi（以及 omp） | npm `@vshulcz/pi-deja` | `pi install npm:@vshulcz/pi-deja` |

兩條路各自都夠用，兩條都走也不會出問題：每個套件都會先讀 `deja install` 寫下的設定。
opencode、dsh 和 OpenClaw 只補上缺的那部分；Kimi、Grok、Codex 和 pi 在安裝器已經接好時
直接讓位；在 Zed 裡兩邊用的是同一個 server id。所以無論先裝哪個都不會重複。

牠們用的都是你已經裝好的 deja，套件裡自帶的那份只是備援。

## 可選的語意召回

用 `DEJA_EMBED_URL` 把 `deja embed` 指向本機的 Ollama、LM Studio 或任何 OpenAI 相容端點，
換個說法提問也能命中。沒有可用的執行環境，詞彙搜尋和 MCP 召回照常運作。

## 證據

```sh
deja bench recall     # 排序回歸下限：100 條查詢，一半是俄語，召回下降時 CI 失敗
deja bench context    # 30 條帶種子的任務鏈，外加五個負對照
deja bench block      # 交出去的那段文字裡還剩不剩答案
deja bench prompt     # 逐條提示的掛鉤在什麼時候開口，又在什麼時候開錯
deja bench ingest     # 一次索引更新的代價：沒有變化、追加一輪、新增一個記錄檔、整檔重寫
```

上下文實驗把 deja 召回與全量歷史、樸素 grep 和冷啟動作對比。預設種子下：

| 方案 | token 中位數 | 覆蓋率中位數 | 負對照 token |
| --- | ---: | ---: | ---: |
| deja-recall | 1,096 | 1.00 | 0 |
| full-history | 80,547 | 1.00 | 78,145 |
| naive-grep | 273,238 | 1.00 | 0 |
| cold | 0 | 0.00 | 0 |

與直接 grep 原始日誌相同的事實覆蓋率，token 少約 250 倍；比完整重播命中的會話少約 70 倍；
在沒有相關歷史的鏈條上則什麼都不注入。語料產生器和相關性標註是普通的、可審閱的 Go 程式碼。
在相信任何數字之前，先審清「相關」是怎麼定義的——包括我們的數字。

在一份真實的儲存庫上測得：2,419 個會話、179k 條訊息，共 1.9 GB 的記錄。

| 指標 | 結果 |
| --- | --- |
| 行程內查詢 | 中位數 **0.7–0.8 ms**，LongMemEval-S 乾草堆上約 19 ms |
| `deja <查詢詞>` 端到端 | 該儲存庫上中位數約 0.2 s：行程啟動、對所有儲存做新鮮度檢查、排序、輸出 |
| 僅新鮮度檢查 | 沒有變化時約 50 ms |
| 索引大小 | 200 MB，約為語料的 10% |

索引是增量的。會話檔案變長時，只重新讀那一個檔案。

## 運作方式

`~/.cache/deja` 裡的本機倒排索引：解析 JSONL 與 SQLite 儲存、遮蔽憑證、寫出 `records.bin`
和詞桶，並在 `manifest.gob` 裡記錄每個檔案的狀態，因此重複執行只會擷取變化的部分。
MCP 伺服器、統計、分享和同步都讀這一份索引。細節見
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。

## 常見問題

**有東西離開我的機器嗎？** 沒有，除非你主動要求。見[資料流向](docs/SECURITY-MODEL.md#data-flows)。

**日誌裡已經有的金鑰怎麼辦？** 牠們留在原本的工具檔案裡，那是你的代理的資料。
牠們不會進入 deja 的索引、摘要、分享或同步匯出。

**會拖慢我的代理嗎？** 一次召回是對本機索引的詞彙查詢：中位數 0.7–0.8 ms，沒有任何東西在等模型。
掛鉤會額外加上行程啟動和對儲存的新鮮度檢查——數 GB 的儲存庫上是幾十毫秒。

**我需要改變工作方式嗎？** 不需要。是代理自己呼叫召回；開啟自動召回後，
會話一打開牠就已經知道這個專案此前的決定。

**和其他記憶工具有什麼不同？**

| | deja | 記憶平台<br>(Mem0、Letta、memU) | 會話檢索<br>(cass) |
| --- | :-: | :-: | :-: |
| 知道安裝它之前的工作 | 是 | 否 | 是 |
| 需要蒐集步驟 | 不需要，逐字稿本身就是記憶 | 由代理或你的程式碼寫入事實 | 不需要 |
| 需要大型模型或嵌入金鑰 | 否 | 是 | 可選 |
| 不用問也會召回 | 會話開始時、以及工具執行前 | 否 | 否 |

[完整比較](https://vshulcz.github.io/deja-vu/guide/compare.html)涵蓋了其中十一個。

**Claude Code 的會話歷史存在哪裡，能搜尋嗎？** 在 `~/.claude/projects` 下，每個會話一個 JSONL 檔；Codex 存在 `~/.codex/sessions`，Cursor 存在 SQLite 的 `state.vscdb`。`deja search` 就地讀取牠們，`deja last` 列出每個代理最近的會話，`deja view` 把全部歷史開成一個本機頁面。各代理的路徑見[會話存在哪裡](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html)。

**Claude Code 的會話歷史不見了，是丟了嗎？** Claude Code 會刪除 30 天以前的記錄（`~/.claude/settings.json` 裡的 `cleanupPeriodDays`），`claude --resume` 只列出還剩下的。deja 在清理前索引過的會話，檔案沒了之後仍然可以搜尋。詳見[磁碟上的會話檔案](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html)。

**怎麼全部清除？**

```sh
deja uninstall --all
rm -rf ~/.cache/deja
```

## 指南

按場景寫的，不是按功能：

- [編碼代理記得之前的對話嗎？](https://vshulcz.github.io/deja-vu/guide/does-my-agent-remember.html)——每個代理在會話之間留下了什麼，又丟掉了什麼
- [磁碟上的會話檔案](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html)——`~/.claude/projects` 會長到多大，刪掉要付出什麼代價
- [代理把你剛才的上下文弄丟了](https://vshulcz.github.io/deja-vu/guide/lost-context.html)——當機之後、清空之後，或者會話回來時空空如也
- [上下文視窗滿了](https://vshulcz.github.io/deja-vu/guide/context-window-full.html)——壓縮到底保住了什麼，實測資料，以及可以換成什麼做法
- [接著昨天的會話做](https://vshulcz.github.io/deja-vu/guide/resume-a-session.html)——跨所有代理找到它，再回到它原來所屬的那一個裡開啟
- [代理又犯了你已經修過的錯](https://vshulcz.github.io/deja-vu/guide/repeated-mistakes.html)
- [找到當初解決它的那次會話](https://vshulcz.github.io/deja-vu/guide/find-a-session.html)
- [為什麼代理在會話之間會忘事](https://vshulcz.github.io/deja-vu/guide/forgetting.html) · [每個代理把歷史存在哪裡](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html)
- [壓縮會丟掉什麼](https://vshulcz.github.io/deja-vu/guide/after-compaction.html) · [換一個代理](https://vshulcz.github.io/deja-vu/guide/switching-agents.html) · [稽核代理做過什麼](https://vshulcz.github.io/deja-vu/guide/auditing-agents.html) · [匯出一次對話](https://vshulcz.github.io/deja-vu/guide/export-conversations.html) · [跨機器](https://vshulcz.github.io/deja-vu/guide/sync-across-machines.html) · [記憶要花多少 token](https://vshulcz.github.io/deja-vu/guide/token-cost.html)

按工具：[opencode](https://vshulcz.github.io/deja-vu/guide/memory-for-opencode.html) · [Zed](https://vshulcz.github.io/deja-vu/guide/memory-for-zed.html) · [Grok Build](https://vshulcz.github.io/deja-vu/guide/memory-for-grok.html) · [Gemini CLI](https://vshulcz.github.io/deja-vu/guide/memory-for-gemini.html) · [OpenClaw](https://vshulcz.github.io/deja-vu/guide/memory-for-openclaw.html) · [Goose](https://vshulcz.github.io/deja-vu/guide/memory-for-goose.html) · [Cline](https://vshulcz.github.io/deja-vu/guide/memory-for-cline.html) · [pi and omp](https://vshulcz.github.io/deja-vu/guide/memory-for-pi.html) · [Hermes](https://vshulcz.github.io/deja-vu/guide/memory-for-hermes.html)

## 在你自己的歷史上試一次

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

裝好十秒，建索引十來秒。下一次代理打開會話，牠就已經知道你在這個專案裡解決過什麼——
包括你裝 deja 之前的那些。

## 參與開發

`make build test lint`，然後看 [CONTRIBUTING.md](CONTRIBUTING.md)。
新增一個工具從[解析器註冊表](docs/ARCHITECTURE.md#source-parsers)開始。
優先順序與非目標見 [ROADMAP.md](ROADMAP.md)。

## 授權

MIT © [Vladislav Shulcz](https://github.com/vshulcz)
