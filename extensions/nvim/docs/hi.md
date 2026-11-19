# deja-vu for Neovim

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)
[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | [Türkçe](tr.md) | हिन्दी

**[deja-vu](https://github.com/vshulcz/deja-vu)** का हिस्सा (**< 1 ms** SQLite FTS5 क्वेरी · **~4%** इंडेक्स आकार · इंडेक्सिंग में **0** LLM टोकन) · [दस्तावेज़](https://vshulcz.github.io/deja-vu/) · [ब्राउज़र डेमो](https://vshulcz.github.io/deja-vu/view/) · [बेंचमार्क](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

सीधे Neovim से Claude Code, Codex CLI, Cursor, opencode, Gemini CLI और 44 अन्य हार्नेस के पुराने कोडिंग एजेंट ट्रांसक्रिप्ट खोजें, लाइन-स्तरीय AI सेशन ब्लेम चलाएँ, एरर फिक्स वापस पाएँ और क्रॉस-एजेंट हैंडऑफ़ सारांश तैयार करें।

![deja-vu डेमो](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## पूर्व-आवश्यकताएँ

[`deja`](https://github.com/vshulcz/deja-vu) बाइनरी इंस्टॉल करें (`>= 0.24`, Neovim `>= 0.9`):

```sh
brew install deja-vu && deja install --auto
```

## इंस्टॉलेशन (`lazy.nvim`)

चूँकि `lua/deja/` और `plugin/deja.lua` रिपॉज़िटरी रूट में हैं, `lazy.nvim` बिना किसी रैपर पैकेज के सीधे `vshulcz/deja-vu` को लोड कर लेता है:

```lua
{
  "vshulcz/deja-vu",
  opts = {
    picker = "auto",        -- "auto" | "snacks" | "telescope" | "fzf" | "select"
    limit = 40,
    project_scoped = false,
    keymaps = true,         -- <leader>dr, <leader>dl, <leader>db, <leader>dh, <leader>df
  },
}
```

[`snacks.nvim`](https://github.com/folke/snacks.nvim), [`telescope.nvim`](https://github.com/nvim-telescope/telescope.nvim) (`:Telescope deja`), [`fzf-lua`](https://github.com/ibhagwan/fzf-lua) या मूल `vim.ui.select` के साथ तुरंत काम करता है।

## कमांड और कीमैप

| कमांड | डिफ़ॉल्ट कीमैप (`keymaps = true`) | क्या चलाता है |
| --- | --- | --- |
| `:DejaRecall [query]` | `<leader>dr` | सभी इंडेक्स किए गए सेशन में पूर्ण-पाठ खोज (`deja search --json`); विज़ुअल चयन समर्थित |
| `:DejaLast [count]` | `<leader>dl` | सभी इंस्टॉल किए गए एजेंट्स के हालिया सेशन (`deja last --json`) |
| `:DejaBlame[!]` | `<leader>db` | कर्सर वाली लाइन किस AI कोडिंग सेशन ने लिखी या बदली (`deja blame file:line --json`; पूरी फ़ाइल के लिए `!`) |
| `:DejaHandoff [id]` | `<leader>dh` | क्रॉस-एजेंट हैंडऑफ़ सारांश कॉपी करें और देखें (`deja handoff`) |
| `:DejaFix [error]` | `<leader>df` | मौजूदा LSP डायग्नोस्टिक या एरर स्ट्रिंग का पिछला समाधान खोजें (`deja fix`) |
| `:DejaShow <id>` | — | फ्लोटिंग Markdown विंडो में सीक्रेट-मुक्त सेशन ट्रांसक्रिप्ट खोलें (`y` ID कॉपी, `H` हैंडऑफ़, `R` स्प्लिट में रीज़्यूम) |

## स्टेटसलाइन (`lualine.nvim`)

```lua
sections = {
  lualine_x = {
    function() return require("deja").statusline() end,
  },
}
```

## CodeCompanion (`codecompanion.nvim`)

`vshulcz/deja-vu` में `lua/codecompanion/_extensions/deja/init.lua` भी शामिल है, जो `/deja` स्लैश कमांड (चैट बफ़र में हैंडऑफ़ जोड़ने वाला इंटरैक्टिव पिकर) और `#{deja}` / `#{deja:query}` एडिटर कॉन्टेक्स्ट पंजीकृत करता है:

```lua
{
  "olimorris/codecompanion.nvim",
  dependencies = { "vshulcz/deja-vu" },
  opts = {
    extensions = {
      deja = { enabled = true },
    },
  },
}
```

बाइनरी खोज और सक्रिय पिकर बैकएंड जाँचने के लिए Neovim में `:checkhealth deja` चलाएँ।

## बेंचमार्क

| रिकॉल (Recall) | टोकन | इंडेक्सिंग और खोज |
|---|---|---|
| LongMemEval-S पर **97.2% R@5** (470 प्रश्नों पर 88.9% hit@1; MemPalace 96.6% और agentmemory 95.2% प्रकाशित करते हैं) | **58% कम टोकन** (दोहराए गए कार्य पर agentmemory के **आधे टोकन**) | 19,195 सेशन इंडेक्स करने में **13.6 s** / 49 कोडिंग एजेंट्स में **0.7 ms** खोज समय (7 में अगला: 72 s) |

## रेपो मेमोरी और एजेंट हैंडऑफ़

जाँचे गए प्रोजेक्ट निर्णय और गलत रास्ते git के ज़रिए `.deja/memory.jsonl` में साझा करें (`deja repo candidates`, `deja repo add <id>`)। एंट्री लिखते समय सीक्रेट्स हटा दिए जाते हैं, `merge=union` के साथ सामग्री हैश के क्रम में सहेजी जाती हैं ताकि समानांतर ब्रांच बिना टकराव के मर्ज हों, और रेपो क्लोन करने वाले हर साथी के लिए लोड होती हैं (ट्रस्ट पॉलिसी में `{"activations":{"auto":{"repo":false}}}` हुक इंजेक्शन बंद करता है)। [और जानें](https://vshulcz.github.io/deja-vu/guide/repo-memory.html)।

काम के बीच में टूल बदल रहे हैं? `deja handoff --to <agent>` चलाएँ (या MCP के ज़रिए `handoff` कॉल करें) ताकि सक्रिय सेशन का लक्ष्य, निर्णय, बदली गई फ़ाइलें, पास हुए चेक और बाकी काम दूसरे कोडिंग एजेंट को मिल सके।

[deja-vu](https://github.com/vshulcz/deja-vu) का हिस्सा। MIT लाइसेंस। उपयोगी लगा? [GitHub पर deja-vu को स्टार दें](https://github.com/vshulcz/deja-vu)।
