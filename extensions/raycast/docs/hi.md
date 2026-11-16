# Raycast के लिए deja-vu

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | हिन्दी

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

**[deja-vu](https://github.com/vshulcz/deja-vu)** का हिस्सा (LongMemEval-S पर **97.2% R@5** · 19,195 सेशन इंडेक्स करने में **13.6 s** · agentmemory के **आधे टोकन**) · [दस्तावेज़](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [लाइव डेमो](https://vshulcz.github.io/deja-vu/view/) · [बेंचमार्क](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub पर स्टार करें](https://github.com/vshulcz/deja-vu)**

Raycast से सीधे Claude Code, Codex, Cursor, OpenCode, Gemini CLI और 43 अन्य कोडिंग एजेंट्स के सेशन खोजें और फिर से शुरू करें।

![deja-vu डेमो](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## पूर्व-आवश्यकताएँ

[`deja-vu`](https://github.com/vshulcz/deja-vu) इंस्टॉल करें:

```sh
brew install deja-vu && deja install --auto
```

## कमांड

- **Search Sessions**: आपके Mac पर हर कोडिंग एजेंट सेशन में फ़ुल-टेक्स्ट खोज (`deja search --json --limit 25 -- <query>`), सटीक मिलान और निकटतम प्रासंगिक परिणामों को अलग दिखाते हुए।
- **Recent Sessions**: सभी इंस्टॉल किए गए एजेंट्स में हालिया 100 सेशन देखें (`deja last 100 --json`), मेटाडेटा पूर्वावलोकन (`Agent`, `Project`, `Started`, `Updated`, `Session ID`) के साथ।

## क्रियाएँ

- **Show Session**: Raycast के भीतर सीक्रेट-मुक्त संवाद पढ़ें (`deja show <id> --json --limit 200`)।
- **Resume in Terminal**: `Terminal.app` में एजेंट की नेटिव रिज़्यूम कमांड (`deja resume <id>`) चलाएँ।
- **Copy Resume Command**, **Copy Session ID** और **Open Project Folder**।

## बेंचमार्क

| रिकॉल (Recall) | टोकन | इंडेक्सिंग और खोज |
|---|---|---|
| LongMemEval-S पर **97.2% R@5** (470 प्रश्नों पर 88.9% hit@1; MemPalace 96.6% और agentmemory 95.2% प्रकाशित करते हैं) | **58% कम टोकन** (दोहराए गए कार्य पर agentmemory के **आधे टोकन**) | 19,195 सेशन इंडेक्स करने में **13.6 s** / 48 कोडिंग एजेंट्स में **0.7 ms** खोज समय (7 में अगला: 72 s) |

## रेपो मेमोरी और एजेंट हैंडऑफ़

जाँचे गए प्रोजेक्ट निर्णय और गलत रास्ते git के ज़रिए `.deja/memory.jsonl` में साझा करें (`deja repo candidates`, `deja repo add <id>`)। एंट्री लिखते समय सीक्रेट्स हटा दिए जाते हैं, `merge=union` के साथ सामग्री हैश के क्रम में सहेजी जाती हैं ताकि समानांतर ब्रांच बिना टकराव के मर्ज हों, और रेपो क्लोन करने वाले हर साथी के लिए लोड होती हैं (ट्रस्ट पॉलिसी में `{"activations":{"auto":{"repo":false}}}` हुक इंजेक्शन बंद करता है)। [और जानें](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)।

काम के बीच में टूल बदल रहे हैं? `deja handoff --to <agent>` चलाएँ (या MCP के ज़रिए `handoff` कॉल करें) ताकि सक्रिय सेशन का लक्ष्य, निर्णय, बदली गई फ़ाइलें, पास हुए चेक और बाकी काम दूसरे कोडिंग एजेंट को मिल सके।

## लोकल विकास

```sh
cd extensions/raycast && npm install && npx ray develop
```

[deja-vu](https://github.com/vshulcz/deja-vu) का हिस्सा। MIT लाइसेंस। उपयोगी लगा? [GitHub पर deja-vu को स्टार दें](https://github.com/vshulcz/deja-vu)।
