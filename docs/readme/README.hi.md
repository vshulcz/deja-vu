<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo-dark.svg">
    <img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo.svg" width="330" alt="deja-vu">
  </picture>
</p>

<p align="center"><b>आपके सारे कोडिंग एजेंट्स की एक साझा मेमोरी, जो आपकी डिस्क पर पहले से पड़े इतिहास से बनती है।</b></p>

<p align="center">आपका एजेंट उसी चीज़ को दोबारा डीबग करने जा रहा है जो आपने मार्च में ठीक की थी — तब किसी और एजेंट में।
deja उन सेशन्स को इंडेक्स करता है जिन्हें Claude Code, Codex, Cursor और इस मशीन के बाकी सारे एजेंट पहले से डिस्क पर
लिखते हैं, और जो भी पूछे उसे सही वाला लौटा देता है।</p>

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif" width="720" alt="एक ही सवाल एक ही एजेंट से दो बार: मेमोरी के बिना उसे कुछ याद नहीं, deja के साथ वह आठ महीने पुराने नतीजे से जवाब देता है"></p>

<p align="center"><sub><em>किसी ने खोजा नहीं — एजेंट ने deja को खुद बुलाया। असली मॉडल और असली टूल कॉल्स के साथ दो असली रन, एक सिंथेटिक कॉर्पस पर: किसी का इतिहास सार्वजनिक नहीं किया जाता।</em></sub></p>

<p align="center"><b>deja पहले ही मिनट से भरी हुई है: वह इतिहास जो 34 एजेंट पहले ही लिख चुके हैं, कुछ सेकंड में इंडेक्स, न कोई मॉडल, न कोई अलग कैप्चर स्टेप।</b></p>

<p align="center">
उसी काम पर जो यह मशीन पहले हल कर चुकी थी, <b>58% कम टोकन</b> &middot; LongMemEval-S (470 सवालों का साफ़ किया सेट) पर <b>88.1% hit@1</b> &middot; LoCoMo पर <b>70.5%</b> &middot; गीगाबाइट इतिहास पर <b>मिलीसेकंड</b> में क्वेरी<br>
<sub>हर भुजा पर ग्यारह रन: कुछ भी जुड़ा न होने पर 126,222 के मुक़ाबले 53,558 टोकन; उसी सेटअप के एक बाद वाले रन में 71% &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/day-zero.html">एक काम पूरा करने की लागत</a> &middot;
दोनों रिट्रीवल हार्नेस इसी रिपॉज़िटरी में हैं और सार्वजनिक डेटासेट पर मिनटों में चलते हैं &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">आँकड़े खुद जाँचिए</a></sub>
</p>

<p align="center"><a href="../../README.md">English</a> | <a href="README.zh.md">简体中文</a> | <a href="README.zh-TW.md">繁體中文</a> | <a href="README.ja.md">日本語</a> | <a href="README.ko.md">한국어</a> | <a href="README.es.md">Español</a> | <a href="README.pt.md">Português</a> | <a href="README.fr.md">Français</a> | <a href="README.de.md">Deutsch</a> | <a href="README.ru.md">Русский</a> | <a href="README.tr.md">Türkçe</a> | हिन्दी</p>

<p align="center"><a href="https://vshulcz.github.io/deja-vu/">दस्तावेज़</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">बेंचमार्क</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/compare.html">तुलना</a></p>
<p align="center"><sub>काम आए तो <a href="https://github.com/vshulcz/deja-vu">GitHub</a> पर deja-vu को स्टार दीजिए।</sub></p>

## इंस्टॉल

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

अगर `raw.githubusercontent.com` तक पहुँच नहीं है, तो उसी वर्शन का npm मिरर है:

```sh
npm i -g @vshulcz/deja-vu --registry=https://registry.npmmirror.com
deja install --auto
```

इंस्टॉल में दस सेकंड, इंडेक्स में करीब दस। दूसरी कमांड हर मिले हुए एजेंट में MCP रिकॉल जोड़ती है, जहाँ एजेंट समर्थन
करता है वहाँ सेशन शुरू होते ही रिकॉल चालू करती है, और पहला इंडेक्स बना देती है — ताकि अगला सेशन किसी चीज़ का
इंतज़ार न करे।

नया एजेंट सेशन खोलिए और महीनों पुरानी किसी चीज़ के बारे में पूछिए:

> क्या हमने पहले jwt refresh rotation पर काम किया था? अपनी मेमोरी में देखो

पूछने की ज़रूरत भी नहीं: ऑटोमैटिक रिकॉल चालू हो तो सेशन खुलते ही एजेंट को पता होता है कि इस प्रोजेक्ट में क्या हल
हो चुका है।

opencode, DeepSeek Harness, Zed, Kimi Code, Codex CLI, Grok Build, OpenClaw और pi के लिए उनके अपने
इकोसिस्टम में पैकेज भी हैं, उन लोगों के लिए जो एक्सटेंशन वहीं से लगाते हैं:

```sh
opencode plugin opencode-deja
dsh plugin --profile web add dsh-deja
# Zed: एक्सटेंशन पैनल में deja खोजिए
# Kimi Code: /plugins install https://github.com/vshulcz/deja-vu
# Codex CLI: codex plugin marketplace add https://github.com/vshulcz/deja-vu && codex plugin add deja-vu@deja-vu
# Grok Build: grok plugin marketplace add xai-org/plugin-marketplace && grok plugin install deja
openclaw plugins install clawhub:@vshulcz/openclaw-deja
pi install npm:@vshulcz/pi-deja
```

`deja install --auto` ऊपर वाला सब कुछ पहले ही जोड़ देता है, इसलिए दोनों में से कोई एक रास्ता काफ़ी है। दोनों साथ
में भी दिक्कत नहीं करते: हर पैकेज पढ़ता है कि `deja install` ने क्या लिखा और सिर्फ़ छूटा हुआ हिस्सा भरता है — न टूल
दो बार रजिस्टर होते हैं, न रिकॉल दो बार चलता है। विवरण [`extensions/`](../../extensions) में है।

यही खोज एक skill के रूप में भी है, जिसे `SKILL.md` पढ़ने वाला कोई भी एजेंट लगा सकता है:

```sh
npx skills add https://github.com/vshulcz/deja-vu --skill deja-search   # skills CLI: Claude Code, Cursor, Goose, Copilot…
openclaw skills install @vshulcz/deja-search                            # ClawHub
hermes skills install vshulcz/deja-vu/skills/deja-search                # Hermes
```

skill आपके इंस्टॉल किए हुए `deja` बाइनरी को बुलाता है, अपना अलग नहीं रखता।

और तरीके: `brew install deja-vu`,
`go install github.com/vshulcz/deja-vu/cmd/deja@latest`, या बिना कुछ इंस्टॉल किए आज़माने के लिए
`npx @vshulcz/deja-vu "क्वेरी"`। Windows पर इंस्टॉल स्क्रिप्ट `unsupported OS` कहकर रुक जाती है क्योंकि वह एक shell
स्क्रिप्ट है; [नवीनतम रिलीज़](https://github.com/vshulcz/deja-vu/releases/latest) से
`deja-vu_<version>_windows_amd64.zip` लीजिए और `deja.exe` को `PATH` में रखिए।

सिर्फ़ बाइनरी भी पूरा इंस्टॉल है: इंडेक्सिंग, खोज, `show`, `ctx`, `blame`, `--json` और क्रेडेंशियल हटाने के लिए और कुछ
नहीं चाहिए। `deja install` का काम है MCP को आपके एजेंट्स से जोड़ना और सेशन शुरू होने पर रिकॉल चालू करना — उपयोगी,
पर वैकल्पिक।

## इससे क्या मिलता है

**Codex में हल हुआ, Claude को याद है।** चौंतीस कोडिंग एजेंट हर बातचीत को लोकल फ़ाइलों में लिखते हैं, और deja उन
फ़ाइलों को एक ऐसी मेमोरी परत में बदल देता है जिसे वे सब पढ़ सकते हैं।

| | |
| --- | --- |
| **पीछे की तरफ़ खोज** | `deja "connection pool exhausted"` गीगाबाइट खंगालता है, deja इंस्टॉल करने से पहले का सब कुछ भी। स्वाभाविक भाषा का सवाल प्रासंगिकता वाले मोड पर चला जाता है। समय एक संकेत है, फ़िल्टर नहीं। |
| **एजेंट्स के आर-पार रिकॉल** | MCP का `deja` टूल `recall` मोड में किसी भी एजेंट के भीतर से जवाब देता है कि «यह हमने तीन हफ़्ते पहले ठीक किया था», चाहे तब किसी ने भी किया हो। |
| **कॉम्पैक्शन के बाद भी बचा रहता है** | 43 कॉन्टेक्स्ट कॉम्पैक्शन पर मापा गया: सारांश ने 77% निर्णय और आपके चलाए हुए कमांड्स का 0.2% बचाया। बाकी 99.8% deja लौटाता है। Claude Code और Codex में deja कॉम्पैक्शन शुरू होते ही काम, फ़ाइलें और कमांड्स दर्ज कर लेता है और अगले सेशन में एक साथ लौटा देता है। |
| **काम करने के ठीक उसी क्षण रिकॉल** | एजेंट किसी फ़ाइल को बदलने या कमांड चलाने से पहले, `PreToolUse` हुक बताता है कि इस फ़ाइल को लेकर पहले क्या तय हुआ था, इस कमांड का वह रूप कौन-सा है जो यहाँ चलता है, या यह कि वह प्रोग्राम इस मशीन पर है ही नहीं। कमांड फ़ेल होने पर `PostToolUse` हुक दिखाता है कि इसी मशीन पर उसी एरर के बाद क्या चलाया गया था — ठीक वही जोड़ी जो एजेंट खुद नहीं पूछेगा। |
| **बातों को नहीं, काम को इंडेक्स करता है** | हर टर्न में खोली गई हर फ़ाइल, चलाया गया हर कमांड और उसका exit code, और वह सटीक हिस्सा जिसे किसी एडिट ने बदला। हर सारांश यही खोता है। |

इसके अलावा: `deja promote <id> --state rejected` पलटे हुए निर्णय पर निशान लगाता है, और उसके बाद हर नतीजा दिखाता है
कि वह आज़माया और ख़ारिज किया गया था; नतीजा बताता है कि «इस सेशन की 4 फ़ाइलें तब से बदल चुकी हैं» और जब तय न कर
सके तो चुप रहता है; `deja sync ssh laptop` मेमोरी को मशीनों के बीच सिर्फ़ जोड़ते हुए ले जाता है, बीच में कोई क्लाउड
नहीं; `deja handoff --to codex` मौजूदा कॉन्टेक्स्ट को बाँध देता है ताकि किसी दूसरे एजेंट में काम आगे बढ़े; जानी-पहचानी
शक्ल वाली कुंजियाँ, टोकन, JWT और प्राइवेट की ब्लॉक इंडेक्सिंग के समय हटा दिए जाते हैं — हालाँकि पैटर्न मिलाना सीक्रेट
पहचानना नहीं है, और कोई अनजानी शक्ल निकल सकती है।

### अपना काम खुद बनाइए

`deja stats --card` सीधे टर्मिनल में बनाता है; फ़ाइल का नाम दीजिए तो आपकी प्रोफ़ाइल README के लिए SVG लिख देता है।
कहीं और प्रकाशित करने के लिए [उसे PNG में बदलिए](https://vshulcz.github.io/deja-vu/card/) — वह पेज आपके अपने
ब्राउज़र में ही बदलता है।

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/docs/assets/stats-card-demo.svg" width="760" alt="deja स्टैट्स कार्ड: एक साल के सेशन हीटमैप में, वे किन एजेंट्स से आए, और सबसे लंबा कौन-सा था"></p>

पूरा रेफ़रेंस [दस्तावेज़ साइट](https://vshulcz.github.io/deja-vu/) पर है।

## निजता

इंडेक्सिंग और खोज लोकल हैं। नेटवर्क सिर्फ़ `deja update`, `deja sync ssh`, `deja doctor` के भीतर वर्शन जाँच, और
`deja embed` इस्तेमाल करते हैं — जो आपके तय किए एंडपॉइंट पर जाता है।

क्रेडेंशियल इंडेक्सिंग के समय हटा दिए जाते हैं: AWS कुंजियाँ, `api_key=` और `token=` असाइनमेंट, bearer टोकन और नंगे
JWT, PEM प्राइवेट की ब्लॉक, अलग-अलग प्रदाताओं के टोकन, `scheme://user:pass@host` रूप वाले URL, ऐसे
हाई-एन्ट्रॉपी मान जिन पर कोई नियम नहीं बैठता, और गद्य में लिखे पासवर्ड — «the admin password is …», जहाँ पकड़ने
के लिए कोई विभाजक ही नहीं होता। मान `[redacted:<kind>]` बन जाता है और आस-पास का पाठ खोजने योग्य बना रहता है।
`deja share` और `deja sync export` निर्यात के समय एक बार और साफ़ करते हैं। पैटर्न मिलाना सीक्रेट पहचानना नहीं है:
जिस शक्ल को नियम नहीं जानते वह ज्यों-की-त्यों निकल सकती है — सुरक्षा मॉडल देखिए।

`deja forget` सेशन्स को दोबारा बने इंडेक्स से हटा देता है और एक समाधि-चिह्न छोड़ता है, ताकि अगला `deja index` उन्हें
मूल इतिहास से वापस न ला सके।
[सुरक्षा मॉडल](../../docs/SECURITY-MODEL.md) में डेटा प्रवाह, सफ़ाई की सीमाएँ, भरोसे की धारणाएँ और रिलीज़ सत्यापन दर्ज हैं।

## कमांड लाइन

```text
$ deja "jwt refresh token"
[claude] api        · Jul 8 · 8f31c0a9 — 2 matches
  login started failing after refresh token rotation; jwt kid mismatch in tests
  fixed by reloading jwks cache after rotateKey and adding a clock-skew test
[codex]  web        · Jul 1 · b77d91e2 — 1 match
  refresh token cookie needed SameSite=Lax in local callback flow
```

| कमांड | क्या करता है |
| --- | --- |
| `deja <क्वेरी>` | पूरे इतिहास में खोजता है। कई शब्द AND हैं, उद्धरण चिह्न लगातार पाठ माँगते हैं; सटीक मिलान न होने पर शब्द-रूप और मिलती-जुलती वर्तनी आज़माता है। |
| `deja wip` | इस डायरेक्टरी में पिछला सेशन क्या कर रहा था: काम, क्या तय हुआ, हाथ में कौन-सी फ़ाइलें थीं, आख़िरी कमांड और वह फ़ेल हुआ या नहीं। सब रिकॉर्ड से निकाला गया, किसी के नोट्स पर निर्भर नहीं। |
| `deja blame <पथ>[:पंक्ति]` | किन सेशन्स ने इस फ़ाइल पर बात की, क्या तय हुआ और क्यों। पंक्ति संख्या देने पर: वह commit जिसने उसे आख़िरी बार बदला, और वह सेशन जिसने उस commit से मिटा दिया गया पाठ लिखा था। |
| `deja files <विषय>` | उलटी दिशा: किसी विषय पर हुए काम ने असल में किन फ़ाइलों को छुआ। |
| `deja how <टूल>` | इस मशीन पर यह असल में कैसे चलाया जाता है, असली आर्ग्युमेंट्स के साथ, उन कमांड्स से जो एजेंट पहले चला चुके हैं। |
| `deja fix <एरर>` | इस मशीन पर उसी एरर के बाद क्या चलाया गया, और जिसके बाद एरर दोबारा नहीं आया। |
| `deja friction` | वे एरर जो तीन से ज़्यादा अलग सेशन्स में मिलते हैं, और वे किन टूल्स से आए। |
| `deja ctx <क्वेरी>` | सबसे अच्छे नतीजों का Markdown सारांश, सीधे प्रॉम्प्ट में डालने लायक। |
| `deja resume <id>` | मिले हुए सेशन को उसी टूल में दोबारा खोलता है जिसका वह है। |
| `deja view` | पूरी मेमोरी को एक लोकल HTML फ़ाइल में निर्यात करता है। कोई सर्वर नहीं, कुछ भी मशीन से बाहर नहीं जाता। |
| `deja doctor [--deep]` | स्व-जाँच; `--deep` के साथ इंडेक्स को स्रोत फ़ाइलों से मिलाकर सत्यापित करता है। |
| `deja mcp` | stdio पर MCP सर्वर — वही जिसे `deja install` जोड़ता है। |

पूरा रेफ़रेंस [कमांड दस्तावेज़](https://vshulcz.github.io/deja-vu/guide/commands.html) में है।

### MCP टूल्स

सर्वर सिर्फ़ एक टूल `deja` देता है, और `mode` पैरामीटर क्षमता चुनता है: `recall`, `context`, `blame`, `fix`,
`how`, `remember`। `deja install` इसे खुद जोड़ देता है, इसलिए यह सिर्फ़ तब मायने रखता है जब आप किसी एजेंट को हाथ
से कॉन्फ़िगर करें। पुराने छह टूल नाम पहले से जुड़े क्लाइंट्स में चलते रहते हैं।

सात के बजाय एक टूल होना लागत का मामला है, शैली का नहीं। जुड़ा हुआ MCP सर्वर अपने टूल्स की परिभाषाएँ हर अनुरोध के
साथ भेजता है, इसलिए एजेंट ने कुछ बुलाया हो या नहीं, हर टर्न में उनका दाम चुकाया जाता है: यहाँ 477 टोकन, जबकि मापे
गए आठ सर्वरों में सबसे बड़े का 8,283। deja का अपना आँकड़ा भी 828 था, जब तक स्कीमा को मोड वाले एक टूल तक नहीं
घटाया गया।

## समर्थित टूल्स

ऑटोमैटिक रिकॉल चालू हो तो Claude Code और Codex कॉम्पैक्शन शुरू होते ही मौजूदा रिकॉर्ड deja को सौंप देते हैं, और
deja वह रख लेता है जो सारांश अभी खोने वाला है: काम, नतीजे, फ़ाइलें, हर कमांड का अंजाम, और क्या बाक़ी रह गया। उसी
सेशन और उसी वर्किंग डायरेक्टरी का अगला हुक इसे एक साथ लौटाता है, 4 KB से ज़्यादा नहीं, और एक पंक्ति में बताता है कि
रिपॉज़िटरी तब से बदली या नहीं। `deja stats` कॉम्पैक्शन और पहली एडिट के बीच टूल कॉल्स गिनता है — इसी से यह मापा
गया था।
विवरण [कॉम्पैक्शन के बाद रिकवरी](../../docs/compaction.md) में है।

Claude Code · Cline · Codex CLI · opencode · aider · Gemini CLI · Cursor · Antigravity ·
Grok Build · Hermes · Goose · Qwen Code · Kimi Code · pi · omp (Oh My Pi) · OpenClaw ·
Copilot CLI · VS Code Copilot Chat · Amp · prime-agent (PrimeIntellect) · Roo Code ·
Continue · Crush · DeepSeek Harness · Cherry Studio · Senpi · gajae-code · Kimchi Coding ·
Command Code · ZCode · CodeWhale · Kiro · Kilo Code · Zed.

इनमें से हर एक क्या समर्थन करता है — MCP रिकॉल, ऑटोमैटिक रिकॉल, skills, कमांड, resume, handoff — यह
[अंग्रेज़ी README की क्षमता तालिका](../../README.md#supported-harnesses) में है। अलग स्टोरेज जगहें `DEJA_*_ROOT`
वेरिएबल्स से बताई जाती हैं, और टूल्स के अपने माइग्रेशन वेरिएबल्स का भी सम्मान किया जाता है।

### अपना पैकेज रखने वाले एजेंट

`deja install --auto` इन्हें भी बाकियों की तरह जोड़ता है, और वही हमेशा सबसे छोटा रास्ता है। साथ ही हर एक का अपने
इकोसिस्टम में एक पैकेज है, उन लोगों के लिए जो एक्सटेंशन वहीं से लगाते हैं:

| एजेंट | पैकेज | इंस्टॉल |
| --- | --- | --- |
| opencode | npm `opencode-deja` | `opencode plugin opencode-deja` |
| DeepSeek Harness | npm `dsh-deja` | `dsh plugin --profile web add dsh-deja` |
| Zed | `deja-context-server` | Zed → Extensions → deja |
| Kimi Code | प्लगइन `deja` | `/plugins install https://github.com/vshulcz/deja-vu` |
| Codex CLI | प्लगइन `deja-vu` | `codex plugin marketplace add https://github.com/vshulcz/deja-vu`, फिर `codex plugin add deja-vu@deja-vu` |
| Grok Build | प्लगइन `deja` | `grok plugin marketplace add xai-org/plugin-marketplace`, फिर `grok plugin install deja` |
| OpenClaw | ClawHub और npm `@vshulcz/openclaw-deja` | `openclaw plugins install clawhub:@vshulcz/openclaw-deja` |
| pi (और omp) | npm `@vshulcz/pi-deja` | `pi install npm:@vshulcz/pi-deja` |

दोनों में से कोई एक रास्ता काफ़ी है और दोनों साथ में भी कुछ नहीं तोड़ते: हर पैकेज पहले वही पढ़ता है जो
`deja install` ने लिखा। opencode, dsh और OpenClaw सिर्फ़ छूटा हुआ भरते हैं; Kimi, Grok, Codex और pi हट जाते हैं
अगर इंस्टॉलर पहले ही जोड़ चुका है; Zed में दोनों तरफ़ एक ही server id इस्तेमाल होता है। इसलिए इंस्टॉल का क्रम मायने
नहीं रखता।

ये सब वही deja इस्तेमाल करते हैं जो आपने पहले से इंस्टॉल किया है; पैकेज के भीतर की प्रति सिर्फ़ बैकअप है।

## वैकल्पिक सिमैंटिक रिकॉल

`DEJA_EMBED_URL` से `deja embed` को लोकल Ollama, LM Studio या किसी भी OpenAI-संगत एंडपॉइंट पर मोड़िए, और
दूसरे शब्दों में पूछा गया सवाल भी लग जाएगा। कोई रनटाइम उपलब्ध न हो तो शाब्दिक खोज और MCP रिकॉल हमेशा की तरह
काम करते हैं।

## प्रमाण

```sh
deja bench recall     # रैंकिंग रिग्रेशन की निचली सीमा: 100 क्वेरी, आधी रूसी में, रिकॉल गिरे तो CI फ़ेल
deja bench context    # बीज सहित 30 टास्क चेन और पाँच नकारात्मक नियंत्रण
deja bench block      # सौंपे गए पाठ-खंड में जवाब अब भी है या नहीं
deja bench prompt     # प्रति-प्रॉम्प्ट हुक कब बोलता है और कब ग़लत बोलता है
deja bench ingest     # एक इंडेक्स अपडेट की लागत: कोई बदलाव नहीं, एक टर्न जुड़ा, एक नई रिकॉर्ड फ़ाइल, पूरी फ़ाइल दोबारा लिखी गई
```

कॉन्टेक्स्ट प्रयोग deja के रिकॉल की तुलना पूरे इतिहास, सीधे grep और कोल्ड स्टार्ट से करता है। डिफ़ॉल्ट बीज पर:

| तरीक़ा | टोकन (माध्यिका) | कवरेज (माध्यिका) | नकारात्मक नियंत्रण के टोकन |
| --- | ---: | ---: | ---: |
| deja-recall | 1,096 | 1.00 | 0 |
| full-history | 80,547 | 1.00 | 78,145 |
| naive-grep | 273,238 | 1.00 | 0 |
| cold | 0 | 0.00 | 0 |

कच्चे लॉग पर grep करने जितनी ही तथ्य-कवरेज, लगभग 250 गुना कम टोकन में; पूरा दोहराकर मिले सेशन्स से करीब 70 गुना
कम; और जिन चेन में प्रासंगिक इतिहास नहीं, उनमें कुछ भी नहीं डाला जाता। कॉर्पस जेनरेटर और प्रासंगिकता लेबलिंग
सामान्य, पढ़ने लायक Go कोड हैं। किसी भी आँकड़े पर भरोसा करने से पहले देखिए कि वहाँ «प्रासंगिक» की परिभाषा क्या है —
हमारे आँकड़ों समेत।

एक असली रिपॉज़िटरी पर मापा गया: 2,419 सेशन, 179k संदेश, 1.9 GB रिकॉर्ड।

| माप | नतीजा |
| --- | --- |
| प्रोसेस के भीतर क्वेरी | माध्यिका **0.7–0.8 ms**, LongMemEval-S के हेस्टैक पर करीब 19 ms |
| पूरा `deja <क्वेरी>` | उस रिपॉज़िटरी पर माध्यिका करीब 0.2 s: प्रोसेस शुरू, सारे स्टोर की ताज़गी जाँच, रैंकिंग, आउटपुट |
| सिर्फ़ ताज़गी जाँच | कुछ न बदला हो तो करीब 50 ms |
| इंडेक्स का आकार | 200 MB, कॉर्पस का करीब 10% |

इंडेक्स इन्क्रीमेंटल है। सेशन फ़ाइल लंबी होने पर सिर्फ़ वही एक फ़ाइल दोबारा पढ़ी जाती है।

## यह कैसे काम करता है

`~/.cache/deja` में एक लोकल इनवर्टेड इंडेक्स: JSONL और SQLite स्टोर पार्स करता है, क्रेडेंशियल साफ़ करता है,
`records.bin` और शब्द-बकेट लिखता है, और हर फ़ाइल की स्थिति `manifest.gob` में रखता है — इसलिए दोबारा चलाने पर
सिर्फ़ बदला हुआ हिस्सा लिया जाता है। MCP सर्वर, स्टैट्स, share और sync सब यही इंडेक्स पढ़ते हैं। विवरण
[docs/ARCHITECTURE.md](../../docs/ARCHITECTURE.md) में है।

## अक्सर पूछे जाने वाले सवाल

**क्या कुछ भी मेरी मशीन से बाहर जाता है?** नहीं, जब तक आप खुद न कहें।
[डेटा प्रवाह](../../docs/SECURITY-MODEL.md#data-flows) देखिए।

**लॉग में जो सीक्रेट पहले से हैं उनका क्या?** वे मूल टूल की फ़ाइलों में ही रहते हैं, वह आपके एजेंट का डेटा है। वे deja
के इंडेक्स, सारांश, share या sync निर्यात में नहीं जाते।

**क्या इससे मेरा एजेंट धीमा होगा?** एक रिकॉल लोकल इंडेक्स पर शाब्दिक क्वेरी है: माध्यिका 0.7–0.8 ms, और कुछ भी
मॉडल का इंतज़ार नहीं करता। हुक प्रोसेस शुरू होने और स्टोर की ताज़गी जाँच जोड़ता है — कई गीगाबाइट की रिपॉज़िटरी पर
कुछ दर्जन मिलीसेकंड।

**क्या मुझे काम करने का तरीक़ा बदलना होगा?** नहीं। रिकॉल एजेंट खुद बुलाता है; ऑटोमैटिक रिकॉल चालू हो तो सेशन खुलते
ही उसे पता होता है कि इस प्रोजेक्ट में पहले क्या तय हुआ था।

**दूसरे मेमोरी टूल्स से यह कैसे अलग है?**

| | deja | मेमोरी प्लेटफ़ॉर्म<br>(Mem0, Letta, memU) | सेशन खोज<br>(cass) |
| --- | :-: | :-: | :-: |
| इंस्टॉल से पहले के काम को जानता है | हाँ | नहीं | हाँ |
| कैप्चर स्टेप चाहिए | नहीं, ट्रांसक्रिप्ट ही मेमोरी है | एजेंट या आपका कोड तथ्य लिखता है | नहीं |
| LLM या एम्बेडिंग कुंजी चाहिए | नहीं | हाँ | वैकल्पिक |
| बिना पूछे याद दिलाता है | सेशन शुरू होने पर, और टूल चलने से पहले | नहीं | नहीं |

[पूरी तुलना](https://vshulcz.github.io/deja-vu/guide/compare.html) इनमें से ग्यारह को कवर करती है।

**Claude Code का सेशन इतिहास कहाँ है और क्या उसे खोजा जा सकता है?** `~/.claude/projects` के नीचे, हर सेशन की एक
JSONL फ़ाइल; Codex `~/.codex/sessions` में, Cursor SQLite `state.vscdb` में। `deja search` उन्हें वहीं पढ़ता है,
`deja last` हर एजेंट का सबसे नया सेशन दिखाता है, और `deja view` पूरे इतिहास को एक लोकल पेज के रूप में खोलता है।
हर एजेंट के पथ
[सेशन कहाँ रखे जाते हैं](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html) में हैं।

**मेरा Claude Code इतिहास ग़ायब हो गया, क्या वह चला गया?** Claude Code 30 दिन से पुराने रिकॉर्ड हटा देता है
(`~/.claude/settings.json` में `cleanupPeriodDays`), और `claude --resume` सिर्फ़ बचा हुआ दिखाता है। सफ़ाई से
पहले deja ने जो सेशन इंडेक्स किए, वे फ़ाइल के ग़ायब होने के बाद भी खोजे जा सकते हैं। विवरण
[डिस्क पर सेशन फ़ाइलें](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html) में है।

**सब कुछ कैसे मिटाऊँ?**

```sh
deja uninstall --all
rm -rf ~/.cache/deja
```

## गाइड

फ़ीचर के हिसाब से नहीं, स्थिति के हिसाब से लिखे गए:

- [क्या कोडिंग एजेंट पिछली बातचीत याद रखता है?](https://vshulcz.github.io/deja-vu/guide/does-my-agent-remember.html) — हर एजेंट सेशन्स के बीच क्या छोड़ता है और क्या खोता है
- [डिस्क पर सेशन फ़ाइलें](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html) — `~/.claude/projects` कितना बड़ा होता है और मिटाने की क़ीमत क्या है
- [एजेंट ने अभी आपका कॉन्टेक्स्ट खो दिया](https://vshulcz.github.io/deja-vu/guide/lost-context.html) — क्रैश के बाद, clear के बाद, या जब सेशन खाली लौटे
- [कॉन्टेक्स्ट विंडो भर गई](https://vshulcz.github.io/deja-vu/guide/context-window-full.html) — कॉम्पैक्शन असल में क्या बचाता है, मापों सहित, और उसकी जगह क्या किया जा सकता है
- [कल का सेशन आगे बढ़ाइए](https://vshulcz.github.io/deja-vu/guide/resume-a-session.html) — सारे एजेंट्स में उसे ढूँढिए और उसी में खोलिए जिसका वह है
- [एजेंट ने वही ग़लती दोहराई जो आप ठीक कर चुके थे](https://vshulcz.github.io/deja-vu/guide/repeated-mistakes.html)
- [वह सेशन ढूँढिए जहाँ यह हल हुआ था](https://vshulcz.github.io/deja-vu/guide/find-a-session.html)
- [एजेंट सेशन्स के बीच क्यों भूलते हैं](https://vshulcz.github.io/deja-vu/guide/forgetting.html) · [हर एजेंट इतिहास कहाँ रखता है](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html)
- [कॉम्पैक्शन क्या खोता है](https://vshulcz.github.io/deja-vu/guide/after-compaction.html) · [एजेंट बदलना](https://vshulcz.github.io/deja-vu/guide/switching-agents.html) · [एजेंट्स ने क्या किया इसका ऑडिट](https://vshulcz.github.io/deja-vu/guide/auditing-agents.html) · [बातचीत निर्यात करना](https://vshulcz.github.io/deja-vu/guide/export-conversations.html) · [मशीनों के बीच](https://vshulcz.github.io/deja-vu/guide/sync-across-machines.html) · [मेमोरी की टोकन लागत](https://vshulcz.github.io/deja-vu/guide/token-cost.html)

टूल के हिसाब से: [opencode](https://vshulcz.github.io/deja-vu/guide/memory-for-opencode.html) · [Zed](https://vshulcz.github.io/deja-vu/guide/memory-for-zed.html) · [Grok Build](https://vshulcz.github.io/deja-vu/guide/memory-for-grok.html) · [Gemini CLI](https://vshulcz.github.io/deja-vu/guide/memory-for-gemini.html) · [OpenClaw](https://vshulcz.github.io/deja-vu/guide/memory-for-openclaw.html) · [Goose](https://vshulcz.github.io/deja-vu/guide/memory-for-goose.html) · [Cline](https://vshulcz.github.io/deja-vu/guide/memory-for-cline.html) · [pi and omp](https://vshulcz.github.io/deja-vu/guide/memory-for-pi.html) · [Hermes](https://vshulcz.github.io/deja-vu/guide/memory-for-hermes.html)

## अपने इतिहास पर आज़माइए

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

इंस्टॉल में दस सेकंड, इंडेक्स में करीब दस। अगली बार जब कोई एजेंट सेशन खोलेगा, उसे पहले से पता होगा कि आपने इस
प्रोजेक्ट में क्या हल किया है — deja इंस्टॉल करने से पहले का भी।

## विकास

`make build test lint`, फिर [CONTRIBUTING.md](../../CONTRIBUTING.md)।
नया टूल [पार्सर रजिस्ट्री](../../docs/ARCHITECTURE.md#source-parsers) से शुरू होता है।
प्राथमिकताएँ और जो हम नहीं करेंगे, वह [ROADMAP.md](../../ROADMAP.md) में है।

## लाइसेंस

MIT © [Vladislav Shulcz](https://github.com/vshulcz)
