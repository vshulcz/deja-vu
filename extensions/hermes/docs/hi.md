# deja-memory for Hermes

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | हिन्दी

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

**[deja-vu](https://github.com/vshulcz/deja-vu)** का हिस्सा (LongMemEval-S पर **97.2% R@5** · 19,195 सेशन इंडेक्स करने में **13.6 s** · agentmemory के **आधे टोकन**) · [दस्तावेज़](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [लाइव डेमो](https://vshulcz.github.io/deja-vu/view/) · [बेंचमार्क](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub पर स्टार करें](https://github.com/vshulcz/deja-vu)**

Hermes के लिए एक मेमोरी प्रोवाइडर जो इस मशीन पर पहले से मौजूद कोडिंग सेशन से उत्तर देता है: Claude Code, Codex, Cursor, स्वयं Hermes और अन्य एजेंट्स जिन्हें deja पढ़ता है—किसी भी चीज़ के इंस्टॉल होने से पहले के महीनों का इतिहास भी शामिल है। न कोई मॉडल, न API कुंजी, न कोई सर्वर। इतिहास एक सिंगल Go बाइनरी [deja](https://github.com/vshulcz/deja-vu) द्वारा लोकल इंडेक्स होता है।

![deja-vu डेमो](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## इंस्टॉल करें

पहले `PATH` पर `deja` होना आवश्यक है:

```bash
brew install deja-vu        # या: curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto         # Hermes और इस मशीन पर हर समर्थित एजेंट को जोड़ता है
hermes plugins install deja-memory   # या GitHub से: hermes plugins install vshulcz/deja-vu/extensions/hermes
hermes config set memory.provider deja-memory
```

या `hermes memory setup` में `deja-memory` चुनें, जो प्रोवाइडर सक्रिय करने से पहले बाइनरी की जाँच करता है।

## यह क्या करता है

- **सेशन के पहले टर्न से पहले**: यह Hermes को वही प्रोजेक्ट सारांश देता है जो deja के हुक्स Claude Code और Codex को देते हैं—इस प्रोजेक्ट में हाल का काम और तय निर्णय। इसके बाद केवल तभी जब अभी पूछा गया प्रश्न पहले की किसी बात से मेल खाए; अधिकतर टर्न में कुछ नहीं जोड़ता।
- **दिखाता है कि क्या लाया गया**: जो पंक्तियाँ deja, Claude Code में दिखाता है (किस एजेंट से मेमोरी आई उसकी रसीद, सेशन की शुरुआत में रीकॉल, बिल्ड और साप्ताहिक नोट्स), वे इंटरैक्टिव CLI और `hermes chat -q` में Hermes की रीकॉल लाइन `🧠 deja · …` पर दिखती हैं। मॉडल का टेक्स्ट पहले जैसा ही रहता है। Hermes 0.17 में रीकॉल लाइन नहीं है, `hermes -z` और `hermes chat -Q` कुछ प्रिंट नहीं करते, TUI इसे केवल स्टेटस बार में पल भर दिखाता है, और गेटवे चैट ऐसा समूह हो सकता है जिसे दूसरे पढ़ते हों—इसलिए वहाँ deja अपने नोट्स ऐसे सेशन के लिए बचाकर रखता है जो उन्हें दिखा सके।
- **cron जॉब में ऑटोमैटिक रीकॉल नहीं**: जब Hermes प्लेटफ़ॉर्म को `cron` बताता है, तो प्रोवाइडर अपने आप कुछ नहीं जोड़ता, लेकिन जॉब द्वारा कॉल किए जाने पर deja टूल्स सामान्य उत्तर देते हैं।
- **तीन टूल्स**: `deja_recall` पुराने सेशन खोजता है, `deja_fix` बताता है कि पिछली बार किसी एरर के बाद क्या चलाया गया, और `deja_blame` किसी फ़ाइल को संपादित करने वाले सेशन सूचीबद्ध करता है।
- **नोट्स की प्राथमिकता**: `MEMORY.md` या `USER.md` में Hermes जो प्रविष्टि जोड़ता है वह deja नोट के रूप में भी सहेजी जाती है, जिससे वह आसपास के सेशन से ऊपर रैंक होती है।
- **कॉम्पैक्शन सुरक्षा**: कॉम्पैक्शन के समय यह सहेज लेता है कि सेशन अगले टर्न के लिए किस काम के बीच में था, और सेशन समाप्त होने पर उसका लाइव स्टैम्प हटा देता है।

एक काम मेमोरी प्रोवाइडर अकेले नहीं कर सकता: अभी विफल हुए कमांड के बगल में पिछला सुधार दिखाना। वह `transform_tool_result` प्लगइन हुक से होता है, और Hermes मेमोरी प्रोवाइडर को ऐसा कॉन्टेक्स्ट देता है जिसमें `register_hook` कुछ नहीं करता (`plugins/memory/__init__.py`, 0.17.0)। `deja install hermes-auto` इसे सँभालने वाला हुक प्लगइन भी लिख देता है।

यह Hermes के टर्न कहीं और नहीं लिखता: Hermes उन्हें पहले से सहेजता है और deja अगली रीफ़्रेश में वहीं से पढ़ता है।

## बेंचमार्क

| रिकॉल (Recall) | टोकन | इंडेक्सिंग और खोज |
|---|---|---|
| LongMemEval-S पर **97.2% R@5** (470 प्रश्नों पर 88.9% hit@1; MemPalace 96.6% और agentmemory 95.2% प्रकाशित करते हैं) | **58% कम टोकन** (दोहराए गए कार्य पर agentmemory के **आधे टोकन**) | 19,195 सेशन इंडेक्स करने में **13.6 s** / 48 कोडिंग एजेंट्स में **0.7 ms** खोज समय (7 में अगला: 72 s) |

## रेपो मेमोरी और एजेंट हैंडऑफ़

जाँचे गए प्रोजेक्ट निर्णय और गलत रास्ते git के ज़रिए `.deja/memory.jsonl` में साझा करें (`deja repo candidates`, `deja repo add <id>`)। एंट्री लिखते समय सीक्रेट्स हटा दिए जाते हैं, `merge=union` के साथ सामग्री हैश के क्रम में सहेजी जाती हैं ताकि समानांतर ब्रांच बिना टकराव के मर्ज हों, और रेपो क्लोन करने वाले हर साथी के लिए लोड होती हैं (ट्रस्ट पॉलिसी में `{"activations":{"auto":{"repo":false}}}` हुक इंजेक्शन बंद करता है)। [और जानें](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)।

काम के बीच में टूल बदल रहे हैं? `deja handoff --to <agent>` चलाएँ (या MCP के ज़रिए `handoff` कॉल करें) ताकि सक्रिय सेशन का लक्ष्य, निर्णय, बदली गई फ़ाइलें, पास हुए चेक और बाकी काम दूसरे कोडिंग एजेंट को मिल सके।

## `deja install` वाला ही कोड

`deja install hermes-auto` इस प्रोवाइडर को MCP सर्वर और `pre_llm_call` हुक प्लगइन के साथ `~/.hermes/plugins/deja-memory` में लिखता है। यहाँ की फ़ाइलें उसी स्रोत (`cmd/deja/install_hermes_memory.go`) से बनती हैं; फ़र्क केवल इतना है कि यह प्रति पाथ हार्डकोड करने के बजाय `PATH` पर `deja` ढूँढती है। दोनों अलग होने पर `TestHermesExtensionIsTheProviderInstallWrites` फ़ेल हो जाता है।

[deja-vu](https://github.com/vshulcz/deja-vu) का हिस्सा। MIT लाइसेंस। उपयोगी लगा? [GitHub पर deja-vu को स्टार दें](https://github.com/vshulcz/deja-vu)।
