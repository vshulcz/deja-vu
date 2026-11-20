# deja-vu for Spec Kit

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | हिन्दी

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** का हिस्सा (LongMemEval-S पर **97.2% R@5**) · [दस्तावेज़](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [लाइव डेमो](https://vshulcz.github.io/deja-vu/view/) · [बेंचमार्क](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

एक [Spec Kit](https://github.com/github/spec-kit) एक्सटेंशन। कोई फ़ीचर इम्प्लीमेंट करने से पहले, `/speckit.deja-vu.recall` आपके पिछले कोडिंग-एजेंट सेशन खोजता है और बताता है कि क्या आज़माया गया, क्या निर्णय लिए गए और क्या विफल रहा, साथ ही हर बिंदु के पीछे सेशन ID जोड़ता है।

ये सेशन [deja](https://github.com/vshulcz/deja-vu) से आते हैं, जो डिस्क पर Claude Code, Codex, Cursor, Copilot, Gemini और अन्य एजेंट्स द्वारा पहले से लिखे गए ट्रांसक्रिप्ट इंडेक्स करता है। सब कुछ आपकी मशीन पर ही रहता है।

![deja-vu डेमो](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## इंस्टॉल करें

पहले `deja` इंस्टॉल करें:

```sh
brew install deja-vu
# या
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

फिर किसी Spec Kit प्रोजेक्ट में जोड़ें:

```sh
specify extension add deja-vu --from https://github.com/vshulcz/deja-vu/releases/latest/download/deja-spec-kit.zip
```

हर deja रिलीज़ में उसी वर्ज़न का `deja-spec-kit.zip` संलग्न होता है। लोकल चेकआउट आज़माने के लिए: `specify extension add --dev /path/to/deja-vu/extensions/spec-kit`।

## उपयोग

```
/speckit.deja-vu.recall
/speckit.deja-vu.recall stripe webhook signature
```

बिना किसी आर्ग्युमेंट के यह सक्रिय फ़ीचर के `spec.md` और `plan.md` पढ़कर खुद क्वेरी चुनता है। आर्ग्युमेंट के साथ उन शब्दों को खोजता है। कमांड का नाम आपके एजेंट की शैली के अनुसार `/speckit-deja-vu-recall` या `$speckit-deja-vu-recall` दिख सकता है।

पीछे रिपॉज़िटरी रूट से यह चलाता है:

```sh
deja recall -- "<query>"   # मिलते-जुलते सेशन, इस प्रोजेक्ट को प्राथमिकता
deja files -- "<query>"    # उस विषय पर खोली या संपादित फ़ाइलें
```

और जब कोई अंश यह समझने के लिए बहुत छोटा हो कि क्या हुआ, तो `deja show <id>` चलाता है। रिपोर्ट एक `Prior sessions` अनुभाग होती है जो आज़माए और विफल, तय निर्णय, फ़ाइलें और खुले कार्यों में बँटी होती है, और हर बिंदु पर `(deja:<id>)` उद्धृत रहता है।

## हुक

यह एक्सटेंशन एक वैकल्पिक `before_implement` हुक पंजीकृत करता है। जब आप `/speckit.implement` चलाते हैं, तो एजेंट पहले रीकॉल चलाने का सुझाव देता है; जब तक आप हाँ न कहें तब तक कुछ नहीं चलता। इसे बंद करने के लिए `.specify/extensions.yml` में प्रविष्टि पर `enabled: false` सेट करें, या `specify extension disable deja-vu` चलाएँ।

## नोट्स

- रीड-ओनली: कमांड केवल खोजता और पढ़ता है, किसी फ़ाइल में बदलाव नहीं करता।
- मशीन पर पहला deja कॉल इंडेक्स बनाता है, जिसमें बड़े इतिहास पर थोड़ा समय लग सकता है। उसके बाद एक सेकंड से बहुत कम समय में उत्तर मिलता है।
- रिकॉल किए गए सेशन टेक्स्ट को संदर्भ डेटा माना जाता है; कमांड एजेंट को सख़्त निर्देश देता है कि उसमें मिले किसी आदेश का पालन न करे।

## बेंचमार्क

| रिकॉल (Recall) | टोकन | इंडेक्सिंग और खोज |
|---|---|---|
| LongMemEval-S पर **97.2% R@5** (470 प्रश्नों पर 88.9% hit@1) | **58% कम टोकन** (दोहराए गए रेपो कार्य पर) | 19,195 सेशन इंडेक्स करने में **13.6 s** / 50 कोडिंग एजेंट्स में **0.7 ms** खोज समय |

## रेपो मेमोरी और एजेंट हैंडऑफ़

जाँचे गए प्रोजेक्ट निर्णय और गलत रास्ते git के ज़रिए `.deja/memory.jsonl` में साझा करें (`deja repo candidates`, `deja repo add <id>`)। एंट्री लिखते समय सीक्रेट्स हटा दिए जाते हैं, `merge=union` के साथ सामग्री हैश के क्रम में सहेजी जाती हैं ताकि समानांतर ब्रांच बिना टकराव के मर्ज हों, और रेपो क्लोन करने वाले हर साथी के लिए लोड होती हैं (ट्रस्ट पॉलिसी में `{"activations":{"auto":{"repo":false}}}` हुक इंजेक्शन बंद करता है)। [और जानें](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)।

काम के बीच में टूल बदल रहे हैं? `deja handoff --to <agent>` चलाएँ (या MCP के ज़रिए `handoff` कॉल करें) ताकि सक्रिय सेशन का लक्ष्य, निर्णय, बदली गई फ़ाइलें, पास हुए चेक और बाकी काम दूसरे कोडिंग एजेंट को मिल सके।

## लाइसेंस

MIT

[deja-vu](https://github.com/vshulcz/deja-vu) का हिस्सा। MIT लाइसेंस।
