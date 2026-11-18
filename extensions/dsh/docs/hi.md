# dsh-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | हिन्दी

**[deja-vu](https://github.com/vshulcz/deja-vu)** का हिस्सा (LongMemEval-S पर **97.2% R@5** · 19,195 सेशन इंडेक्स करने में **13.6 s** · agentmemory के **आधे टोकन**) · [दस्तावेज़](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [लाइव डेमो](https://vshulcz.github.io/deja-vu/view/) · [बेंचमार्क](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu डेमो](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

DeepSeek Harness (`dsh`) और DSH Desktop (`anywhere-labs/dsh-desktop`) के लिए `session-query` प्लगइन। DSH संवादों के साथ-साथ यह एकल Go बाइनरी `deja` और स्थानीय BM25 इंडेक्स से आपके कंप्यूटर पर मौजूद 47 अन्य कोडिंग एजेंट्स (Claude Code, Codex, Cursor, Gemini, Hermes आदि) का इतिहास भी खोजता है (`DEJA_OFFLINE=1` दैनिक अपडेट जाँच बंद करता है)।

## इंस्टॉलेशन

प्लगइन जोड़ते समय `--profile` हमेशा दें:

```sh
dsh plugin --profile web add dsh-deja
# या सीधे रिपॉज़िटरी से:
dsh plugin --profile web add github:vshulcz/deja-vu#path:extensions/dsh
```

यदि `deja` सिस्टम में पहले से नहीं है, तो `DEJA_BIN` सेट करें या सभी 48 एजेंट्स और `$DSH_HOME/plugins/deja/` को एक साथ सेटअप करें:

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
brew install deja-vu && deja install --auto
npx @vshulcz/deja-vu sources
```

## यह क्या जोड़ता है

### छह एजेंट टूल्स

| टूल | उपयोग |
|---|---|
| `deja_recall` | त्रुटि, विषय या फ़्लैग से पुराने सेशन खोजना |
| `deja_session` | मिले परिणाम के आसपास का पूरा संवाद पढ़ना |
| `deja_blame` | फ़ाइल या `path:line` वाले सेशन का पता लगाना |
| `deja_fix` | समान त्रुटि ठीक करने वाला पिछला कमांड पाना |
| `deja_how` | प्रोजेक्ट के वास्तविक कमांड फ़्लैग देखना |
| `deja_remember` | तय निर्णय दर्ज करना |

- **`/deja <...>` कमांड**: प्रॉम्प्ट बॉक्स से सीधे इतिहास खोज।
- **रनटाइम मोड**: `deja install dsh-auto` के साथ `dsh web`, `tui`, `headless` व `sdk` पर उपलब्ध; YAML में `autoRecall: false` से स्वतः इंजेक्शन बंद किया जा सकता है।

## खोज नियंत्रण और शॉर्टकट

`Settings → Deja` से प्रबंधित करें:

- **Mod+F** (या **Mod+Alt+F** / macOS में **Cmd+Option+F**): खोज खोलें; एक सेशन तक सीमित करने हेतु `/deja --session <bare session UUID> -- <your draft>` लिखें।
- **Mod+Shift+K** (macOS **Cmd+Shift+K**): सभी एजेंट्स में वैश्विक खोज।
- **Mod+K**: **DSH conversations only** टॉगल वाले पैनल को खोलता है (अधिकतम 20 परिणाम)।
- **Linux ब्राउज़र विकल्प**: **`Mod+/`**, **`Mod+Shift+,`** और **`Mod+Shift+.`** (`shortcuts.open`)।

## शॉर्टकट बदलना

`Settings → Deja` में **`session.search`** के पास **Change shortcut**, **Restore default** और **Remove** विकल्प मौजूद हैं। **dsh-deja package host plugin** द्वारा `/api/deja-search` संचालित होता है; `/api/deja-search?probe=1` से `{"ok": true, "plugin": "dsh-deja"}` मिलने पर स्थानीय `deja search` कनेक्शन तैयार रहता है।

## तकनीकी वायरिंग

- **सिस्टम प्रॉम्प्ट हुक**: `ctx.systemPrompt.context` और `agent/pre-step` (`dsh 0.1.1-rc.2`+) से `additionalContext` व `systemMessage` भेजता है।
- **API व सीमाएँ**: `/api/deja-notes` (`--plain`) `client.js` को डेटा देता है; `GET /api/deja-search` 20 सेकंड टाइमआउट, 2 MiB आउटपुट सीमा और 500 अक्षर क्वेरी सीमा के साथ `deja search --harness deepseek --json --limit 20 -- <query>` चलाता है।
- **साइडबार स्लॉट**: `data-slot="sidebar.workspaces"`, `aria-expanded` और `_listArea` (DSH `0.2.1-rc.5`)।

## खोज नियंत्रण का परीक्षण

```sh
node --test extensions/dsh/test/*.test.js
```

बिना बिल्ड चरण के ब्राउज़र परीक्षण पेज `extensions/dsh/test/search-browser.html` (React 18 CDN) में शामिल है।

## बेंचमार्क

| रिकॉल (Recall) | टोकन | इंडेक्सिंग और खोज |
|---|---|---|
| LongMemEval-S पर **97.2% R@5** (470 प्रश्नों पर 88.9% hit@1; MemPalace 96.6% और agentmemory 95.2% प्रकाशित करते हैं) | **58% कम टोकन** (दोहराए गए कार्य पर agentmemory के **आधे टोकन**) | 19,195 सेशन इंडेक्स करने में **13.6 s** / 48 कोडिंग एजेंट्स में **0.7 ms** खोज समय (7 में अगला: 72 s) |

## रेपो मेमोरी और एजेंट हैंडऑफ़

जाँचे गए प्रोजेक्ट निर्णय और गलत रास्ते git के ज़रिए `.deja/memory.jsonl` में साझा करें (`deja repo candidates`, `deja repo add <id>`)। एंट्री लिखते समय सीक्रेट्स हटा दिए जाते हैं, `merge=union` के साथ सामग्री हैश के क्रम में सहेजी जाती हैं ताकि समानांतर ब्रांच बिना टकराव के मर्ज हों, और रेपो क्लोन करने वाले हर साथी के लिए लोड होती हैं (ट्रस्ट पॉलिसी में `{"activations":{"auto":{"repo":false}}}` हुक इंजेक्शन बंद करता है)। [और जानें](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)।

काम के बीच में टूल बदल रहे हैं? `deja handoff --to <agent>` चलाएँ (या MCP के ज़रिए `handoff` कॉल करें) ताकि सक्रिय सेशन का लक्ष्य, निर्णय, बदली गई फ़ाइलें, पास हुए चेक और बाकी काम दूसरे कोडिंग एजेंट को मिल सके।

## यह क्या नहीं करता

इंडेक्स केवल `~/.cache/deja/index.db` (`DEJA_INDEX_DIR`) में रहता है, बिना `deja embed` चलाए कोई नेटवर्क कॉल नहीं होती, `sk-*`, `ghp_*`, `AKIA*` व `Bearer` क्रेडेंशियल अपने-आप हटाए जाते हैं, और मूल सेशन फ़ाइलें कभी बदली नहीं जातीं।

## लाइसेंस

[deja-vu](https://github.com/vshulcz/deja-vu) का हिस्सा। MIT लाइसेंस। उपयोगी लगा? [GitHub पर deja-vu को स्टार दें](https://github.com/vshulcz/deja-vu)।
