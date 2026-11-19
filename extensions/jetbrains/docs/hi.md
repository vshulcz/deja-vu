# deja-vu for JetBrains IDEs

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)
[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | हिन्दी

IntelliJ IDEA, GoLand, PyCharm, WebStorm, RustRover, CLion, PhpStorm और Rider (`2024.2+`) के लिए लोकल क्रॉस-एजेंट सेशन सर्च, AI लाइन ब्लेम और हैंडऑफ़ प्लगइन।

[deja-vu](https://github.com/vshulcz/deja-vu) डिस्क पर Claude Code, Codex, OpenCode, Cursor, Gemini CLI, Junie, GitHub Copilot, Aider, Windsurf और 39 अन्य कोडिंग एजेंट्स द्वारा लिखे गए सेशन ट्रांसक्रिप्ट को लोकल SQLite FTS5 में इंडेक्स करता है—आउटपुट से पहले सीक्रेट्स हटाता है और बिना किसी क्लाउड सेवा के काम करता है।

**[★ GitHub पर deja-vu को स्टार दें](https://github.com/vshulcz/deja-vu)** · **[दस्तावेज़](https://vshulcz.github.io/deja-vu/guide/getting-started.html)** · **[बेंचमार्क](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)**

![deja-vu डेमो](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## मुख्य सुविधाएँ

- **स्टेटस बार विजेट**: सक्रिय प्रोजेक्ट के लिए लाइव `deja statusline` आँकड़े IDE स्टेटस बार के दाईं ओर दिखाता है। क्लिक करने पर `deja-vu` एक्शन पॉपअप खुलता है।
- **AI सेशन खोजें (`Tools → deja-vu → Search AI Sessions...`)**: सभी 49 समर्थित कोडिंग एजेंट्स के लोकल सेशन में खोजता है (`deja search --json`) और चुना गया ट्रांसक्रिप्ट रीड-ओनली Markdown एडिटर टैब में खोलता है।
- **हाल के AI सेशन (`Tools → deja-vu → Recent AI Sessions`)**: तेज़ फ़िल्टरिंग के साथ मशीन के सबसे हालिया 50 सेशन (`deja last 50 --json`) सूचीबद्ध करता है।
- **मौजूदा लाइन का AI ब्लेम (एडिटर कॉन्टेक्स्ट मेन्यू / `Tools → deja-vu → AI Blame Current Line`)**: `deja blame <file>:<line> --json` (लाइन न मिलने पर `<file>` फ़ॉलबैक) चलाकर वह AI सेशन खोलता है जिसने कर्सर की लाइन लिखी या बदली थी।
- **क्रॉस-एजेंट हैंडऑफ़ कॉपी करें (`Tools → deja-vu → Copy Cross-Agent Handoff`)**: प्रोजेक्ट रूट में `deja handoff` चलाता है, सारांश को क्लिपबोर्ड पर कॉपी करता है और रीड-ओनली Markdown टैब में खोलता है।

## इंस्टॉलेशन

1. `deja` CLI इंस्टॉल करें और लोकल कोडिंग एजेंट्स जोड़ें:

```sh
brew install deja-vu && deja install --auto
# या Homebrew के बिना:
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
```

2. JetBrains प्लगइन ([JetBrains Marketplace पर `com.github.vshulcz.deja-vu`](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory)) इंस्टॉल करें:

- **JetBrains Marketplace से**: [सीधे plugins.jetbrains.com से इंस्टॉल करें](https://plugins.jetbrains.com/plugin/35998-deja-vu-cross-agent-ai-session-memory), या **Settings → Plugins → Marketplace** में जाकर `deja-vu` खोजें।
- **डिस्क से**: [GitHub Releases](https://github.com/vshulcz/deja-vu/releases/latest) से `deja-jetbrains.zip` डाउनलोड करें और **Settings → Plugins → ⚙️ → Install Plugin from Disk...** चुनें।
- **CLI से**:

```sh
idea installPlugins com.github.vshulcz.deja-vu
```

*(अपनी IDE के अनुसार `idea` को `goland`, `pycharm`, `webstorm`, `rustrover`, या `clion` से बदलें।)*

## सोर्स से बिल्ड करना

```sh
cd extensions/jetbrains
./gradlew buildPlugin
```

इंस्टॉल करने योग्य आर्काइव `build/distributions/` में बनता है।

## बेंचमार्क

| रिकॉल (Recall) | टोकन | इंडेक्सिंग और खोज |
|---|---|---|
| LongMemEval-S पर **97.2% R@5** (470 प्रश्नों पर 88.9% hit@1; MemPalace 96.6% और agentmemory 95.2% प्रकाशित करते हैं) | **58% कम टोकन** (दोहराए गए कार्य पर agentmemory के **आधे टोकन**) | 19,195 सेशन इंडेक्स करने में **13.6 s** / 49 कोडिंग एजेंट्स में **0.7 ms** खोज समय (7 में अगला: 72 s) |

## रेपो मेमोरी और एजेंट हैंडऑफ़

जाँचे गए प्रोजेक्ट निर्णय और गलत रास्ते git के ज़रिए `.deja/memory.jsonl` में साझा करें (`deja repo candidates`, `deja repo add <id>`)। एंट्री लिखते समय सीक्रेट्स हटा दिए जाते हैं, `merge=union` के साथ सामग्री हैश के क्रम में सहेजी जाती हैं ताकि समानांतर ब्रांच बिना टकराव के मर्ज हों, और रेपो क्लोन करने वाले हर साथी के लिए लोड होती हैं (ट्रस्ट पॉलिसी में `{"activations":{"auto":{"repo":false}}}` हुक इंजेक्शन बंद करता है)। [और जानें](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)।

काम के बीच में टूल बदल रहे हैं? `deja handoff --to <agent>` चलाएँ (या MCP के ज़रिए `handoff` कॉल करें) ताकि सक्रिय सेशन का लक्ष्य, निर्णय, बदली गई फ़ाइलें, पास हुए चेक और बाकी काम दूसरे कोडिंग एजेंट को मिल सके।

## लाइसेंस

MIT — [github.com/vshulcz/deja-vu](https://github.com/vshulcz/deja-vu)

उपयोगी लगा? [GitHub पर deja-vu को स्टार दें](https://github.com/vshulcz/deja-vu)।
