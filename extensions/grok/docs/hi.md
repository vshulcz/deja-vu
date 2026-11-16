# Grok Build के लिए deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | हिन्दी

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

**[deja-vu](https://github.com/vshulcz/deja-vu)** का हिस्सा (LongMemEval-S पर **97.2% R@5** · 19,195 सेशन इंडेक्स करने में **13.6 s** · agentmemory के **आधे टोकन**) · [दस्तावेज़](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [लाइव डेमो](https://vshulcz.github.io/deja-vu/view/) · [बेंचमार्क](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub पर स्टार करें](https://github.com/vshulcz/deja-vu)**

![deja-vu डेमो](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

Claude Code, Codex, Cursor, opencode, Zed और अन्य 43 कोडिंग एजेंट्स के स्थानीय सत्र इतिहास से मिलने वाली स्थायी मेमोरी। सब कुछ एकल Go बाइनरी `deja` से लोकल चलता है (`DEJA_OFFLINE=1` दैनिक अपडेट जाँच बंद करता है)।

## इंस्टॉलेशन

पहले `deja` बाइनरी इंस्टॉल करें (प्लगइन पैकेज में नेटिव बाइनरी शामिल नहीं होती):

```sh
brew install deja-vu
# या:
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

फिर इस रिपॉज़िटरी के मार्केटप्लेस से प्लगइन जोड़ें ([xai-org/plugin-marketplace#332](https://github.com/xai-org/plugin-marketplace/pull/332)):

```sh
grok plugin marketplace add vshulcz/deja-vu
grok plugin install deja
```

या सीधे `deja install --auto` चलाएँ जो `~/.grok/config.toml` में `[mcp_servers.deja]` और `~/.grok/hooks/deja.json` लिख देता है।

## आपको क्या मिलता है

- **`deja` MCP टूल** (8 मोड: `recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`)
- **`deja-history` स्किल** और **`/deja:recall <query>`** कमांड
- **5 हुक्स (`hooks/recall.mjs`)**: `SessionStart`, `UserPromptSubmit`, `PreToolUse` (`updatedInput`), `PostToolUse` (Grok 1.0.41 में `Bash` पर), `PreCompact` और `SessionEnd`

## दोनों तरीके साथ रखने पर कोई टकराव नहीं

यदि `~/.grok/config.toml` में पहले से `[mcp_servers.deja]` है या `~/.grok/hooks/deja.json` सेट है, तो प्लगइन स्वतः रुक जाता है (`deja uninstall grok` से CLI वायरिंग हटाई जा सकती है)।

## बाइनरी खोजने का क्रम

`DEJA_BIN` -> `~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin` -> `PATH`.

## बिना बाइनरी के शांत व्यवहार

यह `${GROK_PLUGIN_ROOT}` और `.mcp.json` का उपयोग करता है। Grok Build 1.0.5 पर `deja` न होने की स्थिति में `grok mcp doctor` व `grok inspect` में `handshake failed: connection closed` दिखता है, जो `deja` इंस्टॉल करते ही ठीक हो जाता है।

## बेंचमार्क

| रिकॉल (Recall) | टोकन | इंडेक्सिंग और खोज |
|---|---|---|
| LongMemEval-S पर **97.2% R@5** (470 प्रश्नों पर 88.9% hit@1; MemPalace 96.6% और agentmemory 95.2% प्रकाशित करते हैं) | **58% कम टोकन** (दोहराए गए कार्य पर agentmemory के **आधे टोकन**) | 19,195 सेशन इंडेक्स करने में **13.6 s** / 48 कोडिंग एजेंट्स में **0.7 ms** खोज समय (7 में अगला: 72 s) |

## रेपो मेमोरी और एजेंट हैंडऑफ़

जाँचे गए प्रोजेक्ट निर्णय और गलत रास्ते git के ज़रिए `.deja/memory.jsonl` में साझा करें (`deja repo candidates`, `deja repo add <id>`)। एंट्री लिखते समय सीक्रेट्स हटा दिए जाते हैं, `merge=union` के साथ सामग्री हैश के क्रम में सहेजी जाती हैं ताकि समानांतर ब्रांच बिना टकराव के मर्ज हों, और रेपो क्लोन करने वाले हर साथी के लिए लोड होती हैं (ट्रस्ट पॉलिसी में `{"activations":{"auto":{"repo":false}}}` हुक इंजेक्शन बंद करता है)। [और जानें](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)।

काम के बीच में टूल बदल रहे हैं? `deja handoff --to <agent>` चलाएँ (या MCP के ज़रिए `handoff` कॉल करें) ताकि सक्रिय सेशन का लक्ष्य, निर्णय, बदली गई फ़ाइलें, पास हुए चेक और बाकी काम दूसरे कोडिंग एजेंट को मिल सके।

## ## लाइसेंस

MIT

उपयोगी लगा? [GitHub पर deja-vu को स्टार दें](https://github.com/vshulcz/deja-vu)।
