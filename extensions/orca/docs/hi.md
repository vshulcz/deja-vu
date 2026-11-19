# Orca के लिए deja-vu

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | हिन्दी

`deja` के लिए Orca v2 `trusted-worker` प्लगइन — `deja` इंस्टॉल होने से पहले के सेशन समेत 49 AI कोडिंग टूल्स (Claude Code, Codex, Cursor, opencode, Gemini CLI, Orca आदि) की सेशन मेमोरी को Orca में लाता है।

![deja-vu डेमो](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## इंस्टॉल करें

आवश्यकता: `PATH` पर (या `~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`, `~/bin`, `%LOCALAPPDATA%\Programs\deja` में) [`deja`](https://github.com/vshulcz/deja-vu#install) इंस्टॉल होना चाहिए।

Orca (`>=1.4.248`) में `extensions/orca` प्लगइन डायरेक्टरी इंस्टॉल करें।

Orca के भीतर नेटिव MCP, स्किल फ़ाइलें, `SessionStart`, `PreCompact`, `SessionEnd` और विफल कमांड सुधार कैप्चर के लिए यह भी चलाएँ:

```sh
deja install orca-auto
```

(`deja install orca` बिना लाइफ़साइकिल हुक्स के MCP, स्किल और `/deja` इंस्टॉल करता है; `deja install orca-auto` या `deja install --auto` पूरा हुक सेट जोड़ता है।)

जब `deja install orca-auto` Orca की `settings.json` में नेटिव `UserPromptSubmit` हुक पहले ही लिख चुका होता है, तो यह वर्कर अपने आप पीछे हट जाता है ताकि `<deja-recall>` फ़्रेम दो बार न जुड़ें।

## यह प्लगइन क्या जोड़ता है

- **Hooks**
  - `agent.prompt.before`: सक्रिय वर्कस्पेस पथ निकालता है और Orca के 2s हुक बजट (`1.3s` टाइमआउट, fail-open) के भीतर संबंधित `<deja-recall>` कॉन्टेक्स्ट प्रॉम्प्ट में डालता है।
  - `agent.tool.before`: `Agent` या `Task` सब-एजेंट स्पॉन करते समय केवल `input.prompt` में `<deja-recall>` जोड़ता है (`Bash`, `Read`, `Edit` या अन्य टूल इनपुट नहीं बदलता)।
  - `agent.tool.after`: Orca होस्ट API में केवल अवलोकन-मात्र है (`next` अनदेखा होता है और Orca प्रतीक्षा नहीं करता), इसलिए विफल कमांड पर `fix` डिलीवरी `deja install orca-auto` से आती है।
- **Commands**
  - `deja.orient` (`deja: Orient on workspace`): सक्रिय वर्कस्पेस के लिए `deja hook-context --plain` चलाता है और सारांश सूचना दिखाता है।
  - `deja.recall` (`deja: Recall past sessions`): `deja search --limit 5 -- <query>` (या क्वेरी न देने पर `deja last -n 5`) चलाकर परिणाम दिखाता है।

## बेंचमार्क

| रिकॉल (Recall) | टोकन | इंडेक्सिंग और खोज |
|---|---|---|
| LongMemEval-S पर **97.2% R@5** (470 प्रश्नों पर 88.9% hit@1) | **58% कम टोकन** (दोहराए गए रेपो कार्य पर) | 19,195 सेशन इंडेक्स करने में **13.6 s** / 49 कोडिंग एजेंट्स में **0.7 ms** खोज समय |

## रेपो मेमोरी और एजेंट हैंडऑफ़

जाँचे गए प्रोजेक्ट निर्णय और गलत रास्ते git के ज़रिए `.deja/memory.jsonl` में साझा करें (`deja repo candidates`, `deja repo add <id>`)। एंट्री लिखते समय सीक्रेट्स हटा दिए जाते हैं, `merge=union` के साथ सामग्री हैश के क्रम में सहेजी जाती हैं ताकि समानांतर ब्रांच बिना टकराव के मर्ज हों, और रेपो क्लोन करने वाले हर साथी के लिए लोड होती हैं (ट्रस्ट पॉलिसी में `{"activations":{"auto":{"repo":false}}}` हुक इंजेक्शन बंद करता है)। [और जानें](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)।

काम के बीच में टूल बदल रहे हैं? `deja handoff --to <agent>` चलाएँ (या MCP के ज़रिए `handoff` कॉल करें) ताकि सक्रिय सेशन का लक्ष्य, निर्णय, बदली गई फ़ाइलें, पास हुए चेक और बाकी काम दूसरे कोडिंग एजेंट को मिल सके।

## लाइसेंस

[MIT](../LICENSE)
