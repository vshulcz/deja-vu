# `@vshulcz/magpie-deja` — deja-vu के लिए Magpie गेटवे हुक

[![GitHub Repo stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat-square&logo=github&label=deja-vu)](https://github.com/vshulcz/deja-vu)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | हिन्दी

**[deja-vu](https://github.com/vshulcz/deja-vu)** का हिस्सा (LongMemEval-S पर **97.2% R@5** · 19,195 सेशन इंडेक्स करने में **13.6 s** · agentmemory के **आधे टोकन**) · [दस्तावेज़](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [लाइव डेमो](https://vshulcz.github.io/deja-vu/view/) · [बेंचमार्क](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu डेमो](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

> [Magpie](https://github.com/yetone/magpie) से होकर जाने वाले अनुरोधों में आपकी मशीन पर मौजूद 48 अन्य कोडिंग एजेंट्स (Claude Code, Codex, Cursor, Gemini CLI, OpenClaw, Hermes आदि) का पिछला संदर्भ स्वतः जोड़ता है।

यह सिंगल Go बाइनरी `deja` और स्थानीय BM25 इंडेक्स की मदद से बिना किसी अतिरिक्त LLM के OpenAI (`chat`, `responses`), Anthropic (`anthropic`) और Gemini (`gemini`) प्रोटोकॉल पर काम करता है (`DEJA_OFFLINE=1` दैनिक संस्करण जाँच बंद कर देता है)।

## इंस्टॉलेशन

```sh
magpie plugin add @vshulcz/magpie-deja
```

इसके लिए `deja` बाइनरी (`@vshulcz/deja-vu`, `brew install deja-vu` या `install.sh`) और `deja index` से बना इंडेक्स आवश्यक है। सभी 49 एजेंट्स में `recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember` और `handoff` टूल सेट करने के लिए:

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh && deja install --auto
```

यदि अनुरोध में पहले से `<deja-memory>` या `<deja-recall>` मौजूद है, तो हुक दोहराव से बचने के लिए आगे नहीं बढ़ता।

## कार्यप्रणाली और डेटा सीमा

1. **`onRequest` द्वारा `deja hook-prompt --plain` कॉल**: `~/.cache/deja/index.db` (`DEJA_INDEX_DIR` से बदलें) को `800ms` टाइमआउट के भीतर खोजकर `<deja-memory>` जोड़ता है। **आउटबाउंड अनुरोध चेतावनी**: जोड़ी गई पुरानी जानकारी अपस्ट्रीम LLM प्रदाता को भेजे जाने वाले अनुरोध में शामिल होती है।
2. **कठोर `ctx.cwd` स्कोपिंग**: `allowUnscoped: false` डिफ़ॉल्ट के कारण `deja hook-prompt` कभी भी गेटवे की `process.cwd()` पर फ़ॉलबैक नहीं करता।
3. **स्थानीय स्रोत सीमा**: `recallRemote: false` के साथ यह केवल `ctx.origin === "local"` या `ctx.origin === "magpie-key"` पर चलता है।
4. **दो-स्तरीय सीक्रेट मास्क**: इंडेक्स निर्माण और बाहर जाने से पहले `deja.hooks.mjs` के `maskSecrets` से `sk-*`, `ghp_*`, `github_pat_*`, `AKIA*`, `Bearer`, `PRIVATE KEY`, URL क्रेडेंशियल व `api_key=...` साफ़ किए जाते हैं।
5. **लूप व प्रॉम्प्ट कैश सुरक्षा**: `<deja-memory`, `<deja-recall>` या `[deja —` मिलने पर पुनः नहीं जोड़ता और प्रॉम्प्ट प्रीफ़िक्स कैश सुरक्षित रखता है।

## विकल्प

| विकल्प | डिफ़ॉल्ट | विवरण |
|---|---|---|
| `requestTimeoutMs` | `800` | `deja` प्रक्रिया का टाइमआउट (`50`–`5000` ms) |
| `maxBytes` | `4000` | जोड़े जाने वाले संदर्भ की बाइट सीमा (`256`–`32768`) |
| `recallRemote` | `false` | दूरस्थ अनुरोधों पर रीकॉल सक्षम करें |
| `allowUnscoped` | `false` | `ctx.cwd` न होने पर वैश्विक खोज की अनुमति |
| `cwd` | `""` | प्रोजेक्ट पथ मैन्युअल रूप से ओवरराइड करें |
| `skipAgents` | `[]` | दोहराव रोकने हेतु छूट सूची (जैसे `["claude-code"]`) |
| `bin` | `""` | `deja` बाइनरी का पथ |

## बेंचमार्क

| रिकॉल (Recall) | टोकन | इंडेक्सिंग और खोज |
|---|---|---|
| LongMemEval-S पर **97.2% R@5** (470 प्रश्नों पर 88.9% hit@1; MemPalace 96.6% और agentmemory 95.2% प्रकाशित करते हैं) | **58% कम टोकन** (दोहराए गए कार्य पर agentmemory के **आधे टोकन**) | 19,195 सेशन इंडेक्स करने में **13.6 s** / 49 कोडिंग एजेंट्स में **0.7 ms** खोज समय (7 में अगला: 72 s) |

## रेपो मेमोरी और एजेंट हैंडऑफ़

जाँचे गए प्रोजेक्ट निर्णय और गलत रास्ते git के ज़रिए `.deja/memory.jsonl` में साझा करें (`deja repo candidates`, `deja repo add <id>`)। एंट्री लिखते समय सीक्रेट्स हटा दिए जाते हैं, `merge=union` के साथ सामग्री हैश के क्रम में सहेजी जाती हैं ताकि समानांतर ब्रांच बिना टकराव के मर्ज हों, और रेपो क्लोन करने वाले हर साथी के लिए लोड होती हैं (ट्रस्ट पॉलिसी में `{"activations":{"auto":{"repo":false}}}` हुक इंजेक्शन बंद करता है)। [और जानें](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)।

काम के बीच में टूल बदल रहे हैं? `deja handoff --to <agent>` चलाएँ (या MCP के ज़रिए `handoff` कॉल करें) ताकि सक्रिय सेशन का लक्ष्य, निर्णय, बदली गई फ़ाइलें, पास हुए चेक और बाकी काम दूसरे कोडिंग एजेंट को मिल सके।

## लाइसेंस

[deja-vu](https://github.com/vshulcz/deja-vu) का हिस्सा। MIT लाइसेंस। उपयोगी लगा? [GitHub पर deja-vu को स्टार दें](https://github.com/vshulcz/deja-vu)।
