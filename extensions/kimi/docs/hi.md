# deja for Kimi Code

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | हिन्दी

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** का हिस्सा (LongMemEval-S पर **97.2% R@5**) · [दस्तावेज़](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [लाइव डेमो](https://vshulcz.github.io/deja-vu/view/) · [बेंचमार्क](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

[deja](https://github.com/vshulcz/deja-vu) उन सेशन फ़ाइलों को इंडेक्स करता है जिन्हें कोडिंग एजेंट्स पहले ही डिस्क पर लिखते हैं — Claude Code, Codex, Cursor, opencode और 49 अन्य — और उनसे उत्तर देता है। यह प्लगइन उस इंडेक्स को Kimi Code में लाता है: प्रॉम्प्ट के साथ प्रासंगिक स्मृति आती है, और एजेंट स्वयं भी इतिहास खोज सकता है।

![deja-vu डेमो](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## इंस्टॉल करें

```
/plugins install https://github.com/vshulcz/deja-vu
/reload
```

यह तरीका नवीनतम रिलीज़ को पिन करता है और स्रोत दर्ज करता है, जिसे Kimi की अपडेट जाँच पढ़ती है। पूरे रिपॉज़िटरी के बजाय लगभग 10 KB का `kimi-deja.zip` रिलीज़ पैकेज भी उपलब्ध है।

यदि बाइनरी मौजूद नहीं है तो पहले इसे इंस्टॉल करें (और चाहें तो इस मशीन पर अन्य एजेंट्स भी जोड़ लें):

```
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

प्लगइन प्रबंधित कॉपी से लोड होते हैं, इसलिए इंस्टॉल के बाद `/reload` या `/new` चलाएँ।

## अपडेट

Kimi केवल अपने मार्केटप्लेस से इंस्टॉल किए गए प्लगइन के लिए अपडेट सूचना देता है। रिपॉज़िटरी इंस्टॉल के लिए दोबारा `/plugins install` चलाने पर वर्तमान रिलीज़ आ जाती है, और जब आपकी कॉपी इस deja के साथ आने वाले संस्करण से पुरानी होती है तो `deja doctor` बता देता है:

```
kimi  plugin  ~/.kimi-code/config.toml  (v0.21.2 installed, v0.22.0 ships with this deja — reinstall it in Kimi to update)
```

## आपको क्या मिलता है

- **`deja install kimi-auto` द्वारा लिखे जाने वाले हुक्स**: सेशन के पहले प्रॉम्प्ट पर प्रोजेक्ट के तय निर्णय (`deja hook-context --once`)। हर प्रॉम्प्ट पर पूछे गए विषय के अनुसार रीकॉल (`deja hook-prompt`); कुछ मेल न खाने पर कुछ नहीं जोड़ता। कॉम्पैक्शन से पहले समेटे जा रहे टर्न पढ़कर अगले प्रॉम्प्ट तक अधूरे काम का संदर्भ बनाए रखता है (`deja hook-precompact`)। `SessionEnd` पर सेशन का लाइव स्टैम्प हट जाता है (`deja hook-session-end`)।
- **टूल्स**: प्लगइन `deja mcp` को MCP सर्वर के रूप में घोषित करता है—एक ही `deja` टूल जिसमें `recall`, `context`, `blame`, `fix`, `how`, `orient`, `remember` या `handoff` मोड मिलते हैं।
- **`/deja:recall <query>`**: जब आप सीधे इतिहास से पूछना चाहें।
- **`deja-history` स्किल**: सेशन शुरू होते ही लोड होता है, ताकि एजेंट दोबारा डिबग करने से पहले पिछला इतिहास देख ले।

## कौन-सी बाइनरी चलती है

क्रम से: `DEJA_BIN`, आपकी अपनी इंस्टॉल की हुई `deja` (`~/.local/bin`, `/usr/local/bin`, `/opt/homebrew/bin`, `/usr/bin`), फिर `PATH` पर उपलब्ध `deja`। आपका अपना `deja update` या `brew upgrade deja-vu` हमेशा प्राथमिकता रखता है।

## यदि आपने `deja install kimi` भी चलाया है

CLI `~/.kimi-code/mcp.json` में वही MCP सर्वर और `config.toml` में वही हुक लिखता है। Kimi प्लगइन सर्वर को अपने नेमस्पेस (`plugin-deja:deja`) में चलाता है, इसलिए दोनों चलने पर हर टूल दो बार सूचीबद्ध होता और रीकॉल दो बार जुड़ता।

यह प्लगइन हुक चलने और सर्वर शुरू होने पर इंस्टॉलर की सेटिंग जाँचता है और उसके मिलने पर खुद पीछे हट जाता है—क्योंकि `deja install` वाली कॉपी को ही CLI अद्यतन रखता है। `deja uninstall kimi` बिना दोबारा इंस्टॉल किए स्वामित्व प्लगइन को वापस सौंप देता है।

जब केवल प्लगइन सक्रिय हो, तो `deja doctor` kimi के लिए `plugin` दिखाता है।

दो चीज़ें यह नहीं देख सकता: `deja` के अलावा किसी अन्य नाम से हाथ से जोड़ी गई MCP प्रविष्टि, और बिना इंस्टॉलर के मार्कर कमेंट वाला आपका लिखा कस्टम हुक।

## बेंचमार्क

| रिकॉल (Recall) | टोकन | इंडेक्सिंग और खोज |
|---|---|---|
| LongMemEval-S पर **97.2% R@5** (470 प्रश्नों पर 88.9% hit@1) | **58% कम टोकन** (दोहराए गए रेपो कार्य पर) | 19,195 सेशन इंडेक्स करने में **13.6 s** / 53 कोडिंग एजेंट्स में **0.7 ms** खोज समय |

## रेपो मेमोरी और एजेंट हैंडऑफ़

जाँचे गए प्रोजेक्ट निर्णय और गलत रास्ते git के ज़रिए `.deja/memory.jsonl` में साझा करें (`deja repo candidates`, `deja repo add <id>`)। एंट्री लिखते समय सीक्रेट्स हटा दिए जाते हैं, `merge=union` के साथ सामग्री हैश के क्रम में सहेजी जाती हैं ताकि समानांतर ब्रांच बिना टकराव के मर्ज हों, और रेपो क्लोन करने वाले हर साथी के लिए लोड होती हैं (ट्रस्ट पॉलिसी में `{"activations":{"auto":{"repo":false}}}` हुक इंजेक्शन बंद करता है)। [और जानें](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)।

काम के बीच में टूल बदल रहे हैं? `deja handoff --to <agent>` चलाएँ (या MCP के ज़रिए `handoff` कॉल करें) ताकि सक्रिय सेशन का लक्ष्य, निर्णय, बदली गई फ़ाइलें, पास हुए चेक और बाकी काम दूसरे कोडिंग एजेंट को मिल सके।

## लाइसेंस

MIT

[deja-vu](https://github.com/vshulcz/deja-vu) का हिस्सा। MIT लाइसेंस।
