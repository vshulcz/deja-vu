# @vshulcz/openclaw-deja

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | हिन्दी

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** का हिस्सा (LongMemEval-S पर **97.2% R@5**) · [दस्तावेज़](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [लाइव डेमो](https://vshulcz.github.io/deja-vu/view/) · [बेंचमार्क](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

![deja-vu डेमो](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

आपकी मशीन के 48 अन्य कोडिंग एजेंट्स (Claude Code, Codex, Cursor, Gemini, Zed आदि) का सेशन इतिहास [OpenClaw](https://openclaw.ai) में लाता है। यह सिंगल Go बाइनरी `deja` और स्थानीय BM25 इंडेक्स से बिना LLM के चलता है (`DEJA_OFFLINE=1` दैनिक अपडेट जाँच बंद करता है)।

## इंस्टॉलेशन

```sh
openclaw plugins install clawhub:@vshulcz/openclaw-deja
```

सिस्टम बाइनरी इंस्टॉल करने के लिए:

```sh
brew install deja-vu
```

यदि `deja install openclaw-auto` या `deja install --auto` पहले से कॉन्फ़िगर है, तो दोहराव से बचने के लिए यह प्लगइन स्वतः पीछे हट जाता है।

## प्रमुख क्षमताएँ

- **`agent:bootstrap` (`openclaw agent --local` सहित)**: शुरुआत में ही इस प्रोजेक्ट के पुराने निर्णय जोड़ता है।
- **`before_prompt_build` (OpenClaw `2026.8.1` और `hooks.allowConversationAccess: true` के साथ)**: हर प्रॉम्प्ट से पहले प्रासंगिक पिछला संदर्भ लाता है।
- **`before_compaction`**: कॉम्पैक्शन से पहले वर्तमान कार्य, निष्कर्ष व अधूरे कमांड सहेजता है।
- **`session_end`**: समाप्त हुए सेशन को स्थानीय इंडेक्स में जोड़ता है।
- **टूल रिजल्ट मिडलवेयर**: कमांड विफल होने या किसी फ़ाइल पर काम करते समय पुराने समाधान व निर्णय दिखाता है।
- **`deja_recall` / `deja_fix` / `deja_blame` टूल्स**: एजेंट को इतिहास खोजने, सुधार कमांड पाने और फ़ाइल उत्पत्ति जानने के टूल्स देता है।
- **`/deja <query>`**: चैट से सीधे सभी एजेंट्स के इतिहास में खोज।

### डायरेक्ट-मैसेज स्रोत रसीद (Receipt)

उपयुक्त DM उत्तरों के नीचे `reply_payload_sending` एक इटैलिक स्रोत पंक्ति जोड़ता है:

`_↺ from Codex CLI, 3 weeks ago: "how should the zorbit queue drain on deploy" · deja_`

यह केवल तभी जुड़ती है जब चारों शर्तें पूरी हों:

1. ग्रुप रहित डायरेक्ट संदेश (`isGroup: false` / `direct`)।
2. प्रेषक `commands.ownerAllowFrom` / `allowFrom` से अनुमत हो।
3. ब्लॉक स्ट्रीमिंग बंद हो (`streaming.block.enabled` / `blockStreaming` / `channels.telegram.streaming.mode: "off"`)।
4. यह `direct` चैनल पर अंतिम टेक्स्ट उत्तर हो।

इसे बंद करने के लिए `"receipt": false` सेट करें।

## कॉन्फ़िगरेशन

```json
{
  "plugins": {
    "entries": {
      "deja-vu": {
        "enabled": true,
        "hooks": {
          "allowConversationAccess": true
        },
        "config": {
          "autoRecall": true,
          "tools": true,
          "receipt": true,
          "bin": "/opt/homebrew/bin/deja"
        }
      }
    }
  }
}
```

या CLI द्वारा सक्षम करें:

```sh
openclaw config set plugins.entries.deja-vu.hooks.allowConversationAccess true
```

## बाइनरी समाधान क्रम

`bin` -> `DEJA_BIN` -> `PATH` -> सामान्य इंस्टॉल पथ -> `@vshulcz/deja-vu` की बंडल कॉपी।

## बेंचमार्क

| रिकॉल (Recall) | टोकन | इंडेक्सिंग और खोज |
|---|---|---|
| LongMemEval-S पर **97.2% R@5** (470 प्रश्नों पर 88.9% hit@1) | **58% कम टोकन** (दोहराए गए रेपो कार्य पर) | 19,195 सेशन इंडेक्स करने में **13.6 s** / 49 कोडिंग एजेंट्स में **0.7 ms** खोज समय |

## रेपो मेमोरी और एजेंट हैंडऑफ़

जाँचे गए प्रोजेक्ट निर्णय और गलत रास्ते git के ज़रिए `.deja/memory.jsonl` में साझा करें (`deja repo candidates`, `deja repo add <id>`)। एंट्री लिखते समय सीक्रेट्स हटा दिए जाते हैं, `merge=union` के साथ सामग्री हैश के क्रम में सहेजी जाती हैं ताकि समानांतर ब्रांच बिना टकराव के मर्ज हों, और रेपो क्लोन करने वाले हर साथी के लिए लोड होती हैं (ट्रस्ट पॉलिसी में `{"activations":{"auto":{"repo":false}}}` हुक इंजेक्शन बंद करता है)। [और जानें](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)।

काम के बीच में टूल बदल रहे हैं? `deja handoff --to <agent>` चलाएँ (या MCP के ज़रिए `handoff` कॉल करें) ताकि सक्रिय सेशन का लक्ष्य, निर्णय, बदली गई फ़ाइलें, पास हुए चेक और बाकी काम दूसरे कोडिंग एजेंट को मिल सके।

## गोपनीयता

इंडेक्स `~/.cache/deja/index.db` (`DEJA_INDEX_DIR`) में संग्रहीत होता है और `sk-*`, `ghp_*`, `AKIA*`, `Bearer` इंडेक्सिंग के समय हटा दिए जाते हैं। बिना `deja embed` के सब ऑफ़लाइन रहता है। सभी 49 एजेंट्स एक साथ जोड़ने के लिए `deja install --auto` चलाएँ।

[deja-vu](https://github.com/vshulcz/deja-vu) का हिस्सा। MIT लाइसेंस।
