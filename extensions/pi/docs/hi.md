# @vshulcz/pi-deja

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | हिन्दी

**[deja-vu](https://github.com/vshulcz/deja-vu)** का हिस्सा (LongMemEval-S पर **97.2% R@5**) · [दस्तावेज़](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [लाइव डेमो](https://vshulcz.github.io/deja-vu/view/) · [बेंचमार्क](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu डेमो](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

[pi](https://github.com/badlogic/pi-mono) के लिए स्थानीय मेमोरी एक्सटेंशन, जो आपके कंप्यूटर पर मौजूद 48 अन्य कोडिंग एजेंट्स (Claude Code, Codex, Cursor, Gemini, OpenClaw, Hermes आदि) के डिस्क सत्रों से संदर्भ जोड़ता है। यह सिंगल Go बाइनरी `deja` और स्थानीय BM25 इंडेक्स पर बिना किसी LLM या एम्बेडिंग के चलता है; केवल अपडेट/सिंक कमांड और `DEJA_OFFLINE=1` से बंद हो सकने वाली दैनिक रिलीज़ जाँच के अलावा कोई नेटवर्क कॉल नहीं होती।

## इंस्टॉलेशन

```sh
pi install npm:@vshulcz/pi-deja
```

यदि सिस्टम में `deja` पहले से मौजूद नहीं है, तो यह पैकेज [कौन-सी बाइनरी उपयोग होती है](#कौन-सी-बाइनरी-उपयोग-होती-है) के क्रम के अनुसार साथ वाली कॉपी का उपयोग करता है। सिस्टम बाइनरी पहले इंस्टॉल करने के लिए:

```sh
brew install deja-vu
```

यदि `deja install pi-auto` या `deja install --auto` के ज़रिए स्टैंडअलोन एक्सटेंशन पहले से सेट है, तो दोहराव से बचने के लिए यह पैकेज स्वतः पीछे हट जाता है। `pi.extensions` और `before_agent_start` के ज़रिए यही व्यवहार `omp` में भी काम करता है।

## यह क्या करता है

- **सेशन की शुरुआत (फ़ुटर स्टेटस बार)**: वर्तमान प्रोजेक्ट के इंडेक्स किए गए सेशन और हिट स्थिति दिखाता है।
- **हर प्रॉम्प्ट पर (`before_agent_start`)**: मॉडल के उत्तर से पहले इस प्रोजेक्ट के पुराने निर्णय और समाधान खोजकर जोड़ता है।
- **असफल कमांड के बाद (`bash` / `powershell` का `tool_result`)**: पिछली बार इसी त्रुटि को ठीक करने वाला कमांड दिखाता है।
- **फ़ाइल पढ़ते या संपादित करते समय (`tool_result`)**: संबंधित फ़ाइल से जुड़े पिछले सेशन के निर्णय याद दिलाता है।
- **कॉम्पैक्शन के बाद (`session_compact`)**: सारांश बनने के बाद अधूरे कार्य, निर्णय और कमांड दोबारा जोड़ता है।
- **सेशन समाप्त होने पर (`session_shutdown`)**: नए सेशन को स्थानीय इंडेक्स में अपडेट करता है।
- **`/deja <query>`**: चैट से सीधे सभी एजेंट्स का इतिहास खोजें।

## कौन-सी बाइनरी उपयोग होती है

`deja` बाइनरी इस क्रम में खोजी जाती है:

1. `DEJA_BIN` एनवायरनमेंट वेरिएबल
2. `PATH` में मौजूद `deja`
3. सामान्य इंस्टॉल पथ (`~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin` आदि)
4. `@vshulcz/deja-vu` npm पैकेज में शामिल बाइनरी

## बेंचमार्क

| रिकॉल (Recall) | टोकन | इंडेक्सिंग और खोज |
|---|---|---|
| LongMemEval-S पर **97.2% R@5** (470 प्रश्नों पर 88.9% hit@1) | **58% कम टोकन** (दोहराए गए रेपो कार्य पर) | 19,195 सेशन इंडेक्स करने में **13.6 s** / 50 कोडिंग एजेंट्स में **0.7 ms** खोज समय |

## रेपो मेमोरी और एजेंट हैंडऑफ़

जाँचे गए प्रोजेक्ट निर्णय और गलत रास्ते git के ज़रिए `.deja/memory.jsonl` में साझा करें (`deja repo candidates`, `deja repo add <id>`)। एंट्री लिखते समय सीक्रेट्स हटा दिए जाते हैं, `merge=union` के साथ सामग्री हैश के क्रम में सहेजी जाती हैं ताकि समानांतर ब्रांच बिना टकराव के मर्ज हों, और रेपो क्लोन करने वाले हर साथी के लिए लोड होती हैं (ट्रस्ट पॉलिसी में `{"activations":{"auto":{"repo":false}}}` हुक इंजेक्शन बंद करता है)। [और जानें](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)।

काम के बीच में टूल बदल रहे हैं? `deja handoff --to <agent>` चलाएँ (या MCP के ज़रिए `handoff` कॉल करें) ताकि सक्रिय सेशन का लक्ष्य, निर्णय, बदली गई फ़ाइलें, पास हुए चेक और बाकी काम दूसरे कोडिंग एजेंट को मिल सके।

## गोपनीयता

इंडेक्स `~/.cache/deja/index.db` में रहता है (`DEJA_INDEX_DIR` से बदल सकते हैं) और `sk-*`, `ghp_*`, `AKIA*` तथा `Bearer` जैसे क्रेडेंशियल इंडेक्सिंग के समय साफ़ कर दिए जाते हैं। जब तक आप स्वयं `deja embed` कॉन्फ़िगर नहीं करते, सारा काम ऑफ़लाइन रहता है। सभी 50 एजेंट्स में `recall`, `fix`, `blame` और `handoff` सेट करने के लिए `deja install --auto` चलाएँ।

## लाइसेंस

[deja-vu](https://github.com/vshulcz/deja-vu) का हिस्सा। MIT लाइसेंस।
