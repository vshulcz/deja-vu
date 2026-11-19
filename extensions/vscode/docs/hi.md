# VS Code, Cursor, Windsurf और Antigravity के लिए deja-vu

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | हिन्दी

**आपके कोडिंग एजेंट्स उन समस्याओं को बार-बार डीबग करना बंद कर देते हैं जिन्हें आप पहले ही सुलझा चुके हैं।**

[deja-vu](https://github.com/vshulcz/deja-vu) उन सेशन ट्रांसक्रिप्ट्स को लोकल स्तर पर इंडेक्स करता है जिन्हें Claude Code, Cursor, Codex, Windsurf, Copilot Chat, OpenCode, Cline, Roo Code और 41 अन्य कोडिंग एजेंट्स पहले ही डिस्क पर लिख चुके हैं — एक Go बाइनरी में, बिना LLM और बिना बैकग्राउंड डेमन के।

**[★ GitHub पर स्टार करें](https://github.com/vshulcz/deja-vu)** · **[दस्तावेज़](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[बेंचमार्क](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![deja-vu डेमो](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## यह एक्सटेंशन क्या करता है

IDE स्टेटस बार में लाइव `deja statusline` आँकड़े दिखाता है (एजेंट्स में इंडेक्स किए गए सेशन, आज दिए गए मेमोरी रिकॉल और वर्कस्पेस इतिहास)। यदि `deja` अभी इंस्टॉल नहीं है, तो स्टेटस बार आइटम पर क्लिक करने से टर्मिनल में `npx -y @vshulcz/deja-vu install --auto` चलकर मशीन पर मिले सभी एजेंट्स जुड़ जाते हैं।

## त्वरित इंस्टॉल

```sh
brew install deja-vu && deja install --auto
# या Homebrew के बिना:
npx -y @vshulcz/deja-vu install --auto
```

CLI से स्टेटस बार एक्सटेंशन इंस्टॉल करें:

```sh
code --install-extension vshulcz.deja-status
```

## मापा गया प्रभाव

| बेंचमार्क | परिणाम |
|---|---|
| LongMemEval-S रिट्रीवल सटीकता (470 साफ़ प्रश्न) | **97.2% R@5**, **88.9% hit@1** (MemPalace 96.6%, agentmemory 95.2%) |
| दोहराए गए रेपो काम पर टोकन लागत | **58% कम टोकन** — agentmemory से आधे (`52,815` बनाम बिना मेमोरी `103,443`, 11 युग्मित रन) |
| इन-प्रोसेस लेक्सिकल क्वेरी लेटेंसी | 49 कोडिंग एजेंट्स में माध्यिका **0.7–0.8 ms** |
| 19,195 वास्तविक सेशन की प्रारंभिक इंडेक्सिंग | **13.6 s**, कोई API कुंजी नहीं (7 में अगला: 72 s) |

## रेपो मेमोरी और एजेंट हैंडऑफ़

जाँचे गए प्रोजेक्ट निर्णय और गलत रास्ते git के ज़रिए `.deja/memory.jsonl` में साझा करें (`deja repo candidates`, `deja repo add <id>`)। एंट्री लिखते समय सीक्रेट्स हटा दिए जाते हैं, `merge=union` के साथ सामग्री हैश के क्रम में सहेजी जाती हैं ताकि समानांतर ब्रांच बिना टकराव के मर्ज हों, और रेपो क्लोन करने वाले हर साथी के लिए लोड होती हैं (ट्रस्ट पॉलिसी में `{"activations":{"auto":{"repo":false}}}` हुक इंजेक्शन बंद करता है)। [और जानें](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)।

काम के बीच में टूल बदल रहे हैं? `deja handoff --to <agent>` चलाएँ (या MCP के ज़रिए `handoff` कॉल करें) ताकि सक्रिय सेशन का लक्ष्य, निर्णय, बदली गई फ़ाइलें, पास हुए चेक और बाकी काम दूसरे कोडिंग एजेंट को मिल सके।

## लाइसेंस

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)

[deja-vu](https://github.com/vshulcz/deja-vu) का हिस्सा। MIT लाइसेंस। उपयोगी लगा? [GitHub पर deja-vu को स्टार दें](https://github.com/vshulcz/deja-vu)।
