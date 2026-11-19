# CodeWhale के लिए deja-vu

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | हिन्दी

CodeWhale के लिए Claude-संगत प्लगइन बंडल — 49 AI कोडिंग टूल्स के सेशन पर `deja` MCP सर्वर (`recall`, `fix`, `blame`, `how`, `orient`, `context`, `remember`, `handoff`), `deja-history` स्किल और `/deja` कमांड उपलब्ध कराता है।

![deja-vu डेमो](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## इंस्टॉल करें

आवश्यकता: `PATH` में [`deja`](https://github.com/vshulcz/deja-vu#install) इंस्टॉल होना चाहिए।

CodeWhale में `/plugin install` की मदद से रेपो की उप-डायरेक्टरी (`extensions/codewhale`) से यह बंडल इंस्टॉल करें।

CodeWhale का Claude प्लगइन संगतता लोडर MCP सर्वर, स्किल्स और स्लैश कमांड आयात करता है लेकिन `hooks` और `lspServers` को अनदेखा कर देता है। यदि आप `~/.codewhale/config.toml` में नेटिव CodeWhale लाइफ़साइकिल हुक्स (`message_submit`, `tool_call_before`, `tool_call_after`, `session_end`) भी जोड़ना चाहते हैं, तो `deja install codewhale-auto` (या `deja install --auto`) चलाएँ (`deja install codewhale` केवल MCP और `tools.always_load` सेट करता है):

```sh
deja install codewhale-auto
```

## बंडल सामग्री

- `.claude-plugin/plugin.json`: प्लगइन मेटाडेटा (`name: deja-vu`)
- `.mcp.json`: `deja mcp` stdio सर्वर प्रविष्टि
- `skills/deja-history/SKILL.md`: `deja` मोड्स के लिए स्किल निर्देश
- `commands/deja.md`: `/deja` स्लैश कमांड

## बेंचमार्क

| रिकॉल (Recall) | टोकन | इंडेक्सिंग और खोज |
|---|---|---|
| LongMemEval-S पर **97.2% R@5** (470 प्रश्नों पर 88.9% hit@1) | **58% कम टोकन** (दोहराए गए रेपो कार्य पर) | 19,195 सेशन इंडेक्स करने में **13.6 s** / 49 कोडिंग एजेंट्स में **0.7 ms** खोज समय |

## रेपो मेमोरी और एजेंट हैंडऑफ़

जाँचे गए प्रोजेक्ट निर्णय और गलत रास्ते git के ज़रिए `.deja/memory.jsonl` में साझा करें (`deja repo candidates`, `deja repo add <id>`)। एंट्री लिखते समय सीक्रेट्स हटा दिए जाते हैं, `merge=union` के साथ सामग्री हैश के क्रम में सहेजी जाती हैं ताकि समानांतर ब्रांच बिना टकराव के मर्ज हों, और रेपो क्लोन करने वाले हर साथी के लिए लोड होती हैं (ट्रस्ट पॉलिसी में `{"activations":{"auto":{"repo":false}}}` हुक इंजेक्शन बंद करता है)। [और जानें](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)।

काम के बीच में टूल बदल रहे हैं? `deja handoff --to <agent>` चलाएँ (या MCP के ज़रिए `handoff` कॉल करें) ताकि सक्रिय सेशन का लक्ष्य, निर्णय, बदली गई फ़ाइलें, पास हुए चेक और बाकी काम दूसरे कोडिंग एजेंट को मिल सके।

## लाइसेंस

[MIT](../LICENSE)
