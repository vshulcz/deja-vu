# opencode-deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | हिन्दी

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** का हिस्सा (LongMemEval-S पर **97.2% R@5**) · [दस्तावेज़](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [लाइव डेमो](https://vshulcz.github.io/deja-vu/view/) · [बेंचमार्क](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu डेमो](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

[opencode](https://opencode.ai) के लिए आपके कंप्यूटर पर मौजूद 48 अन्य कोडिंग एजेंट्स (Claude Code, Codex, Cursor, Gemini, Hermes, Zed आदि) के इतिहास पर आधारित स्थानीय मेमोरी। यह एकल Go बाइनरी `deja` और स्थानीय BM25 इंडेक्स से बिना LLM के चलता है (`DEJA_OFFLINE=1` दैनिक अपडेट जाँच बंद करता है)।

## इंस्टॉलेशन

अपनी `opencode.ai/config.json` में जोड़ें:

```json
{ "plugin": ["opencode-deja"] }
```

(opencode 2.x में `"plugins": ["opencode-deja"]` या `opencode plugin add opencode-deja` का उपयोग करें।) या `deja install --auto` चलाकर `opencode.json` व `plugins/deja.js` स्वतः सेट करें।

## इसमें क्या मिलता है

### छह एजेंट टूल्स

| टूल | उपयोग |
|---|---|
| `deja_recall` | त्रुटि, प्रतीक या विषय से पुराने सेशन खोजना |
| `deja_session` | मिले हुए परिणाम के आसपास के संवाद पढ़ना |
| `deja_blame` | यह पता लगाना कि फ़ाइल या `path:line` किस सेशन में लिखी गई |
| `deja_fix` | पिछली बार इसी त्रुटि को ठीक करने वाला कमांड देखना |
| `deja_how` | प्रोजेक्ट में उपयोग हुए सटीक फ़्लैग याद करना |
| `deja_remember` | तय निर्णय को आगे के लिए सहेजना |

### चार स्वचालित रीकॉल व्यवहार

- **सेशन स्टार्ट संकेत**: opencode 1.x में संक्षिप्त टोस्ट और 2.x में सिस्टम प्रॉम्प्ट (`deja-vu — from Codex CLI, 4 months ago: "…"`).
- **20 मिनट कूलडाउन और सब-एजेंट स्किप**: एक ही प्रोजेक्ट में बार-बार दोहराव नहीं करता और सब-एजेंट्स पर बोझ नहीं डालता।
- **विफल कमांड व फ़ाइल निर्णय**: कमांड त्रुटि के बाद पिछला समाधान और फ़ाइल के साथ पुराने निर्णय जोड़ता है।
- **प्री-कॉम्पैक्ट सुरक्षा**: सारांश से पहले मुख्य कार्य, निर्णय और अधूरे कमांड बचाता है।

## विकल्प

```json
{
  "plugin": [["opencode-deja", { "autoRecall": false, "tools": true }]]
}
```

(2.x में `"plugins"` कुंजी का उपयोग करें।)

- `autoRecall`: स्वचालित संदर्भ जोड़ना चालू/बंद करता है (डिफ़ॉल्ट `true`)।
- `tools`: सभी 6 टूल्स उपलब्ध कराता है (डिफ़ॉल्ट `true`)।
- `bin`: कस्टम `deja` बाइनरी पथ।

सभी 50 टूल्स में 8 MCP मोड (`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`) सेट करने के लिए `deja install --auto` चलाएँ ([बिना संदर्भ खोए एजेंट बदलें](https://vshulcz.github.io/deja-vu/guide/switch-agents-without-losing-context.html))।

## कौन-सी बाइनरी उपयोग होती है

`bin` -> `DEJA_BIN` -> `PATH` -> `~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin` -> npm पैकेज की कॉपी।

## गोपनीयता

इंडेक्स `~/.cache/deja/index.db` में रहता है (`DEJA_INDEX_DIR` से बदल सकते हैं) और `sk-*`, `ghp_*`, `AKIA*`, `Bearer` इंडेक्स बनते समय ही साफ़ कर दिए जाते हैं। `deja embed` के बिना सब ऑफ़लाइन रहता है।

## बेंचमार्क

| रिकॉल (Recall) | टोकन | इंडेक्सिंग और खोज |
|---|---|---|
| LongMemEval-S पर **97.2% R@5** (470 प्रश्नों पर 88.9% hit@1) | **58% कम टोकन** (दोहराए गए रेपो कार्य पर) | 19,195 सेशन इंडेक्स करने में **13.6 s** / 50 कोडिंग एजेंट्स में **0.7 ms** खोज समय |

## रेपो मेमोरी और एजेंट हैंडऑफ़

जाँचे गए प्रोजेक्ट निर्णय और गलत रास्ते git के ज़रिए `.deja/memory.jsonl` में साझा करें (`deja repo candidates`, `deja repo add <id>`)। एंट्री लिखते समय सीक्रेट्स हटा दिए जाते हैं, `merge=union` के साथ सामग्री हैश के क्रम में सहेजी जाती हैं ताकि समानांतर ब्रांच बिना टकराव के मर्ज हों, और रेपो क्लोन करने वाले हर साथी के लिए लोड होती हैं (ट्रस्ट पॉलिसी में `{"activations":{"auto":{"repo":false}}}` हुक इंजेक्शन बंद करता है)। [और जानें](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)।

काम के बीच में टूल बदल रहे हैं? `deja handoff --to <agent>` चलाएँ (या MCP के ज़रिए `handoff` कॉल करें) ताकि सक्रिय सेशन का लक्ष्य, निर्णय, बदली गई फ़ाइलें, पास हुए चेक और बाकी काम दूसरे कोडिंग एजेंट को मिल सके।

## ## लाइसेंस

MIT
