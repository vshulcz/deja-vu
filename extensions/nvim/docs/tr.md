# deja-vu for Neovim

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)
[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | Türkçe | [हिन्दी](hi.md)

**[deja-vu](https://github.com/vshulcz/deja-vu)** parçası (**< 1 ms** SQLite FTS5 araması · **~%4** indeks boyutu · indekslemede **0** LLM token) · [Belgeler](https://vshulcz.github.io/deja-vu/) · [Tarayıcı demosu](https://vshulcz.github.io/deja-vu/view/) · [Ölçümler](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Doğrudan Neovim içinden Claude Code, Codex CLI, Cursor, opencode, Gemini CLI ve 48 diğer araçtaki eski kodlama ajanı kayıtlarını arayın, satır düzeyinde AI oturum blame çalıştırın, hata çözümlerini çağırın ve ajanlar arası devir özetleri üretin.

![deja-vu demosu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Ön koşullar

[`deja`](https://github.com/vshulcz/deja-vu) ikilisini kurun (`>= 0.24`, Neovim `>= 0.9`):

```sh
brew install deja-vu && deja install --auto
```

## Kurulum (`lazy.nvim`)

`lua/deja/` ve `plugin/deja.lua` doğrudan depo kökünde bulunduğu için `lazy.nvim`, ek sarmalayıcı paket olmadan `vshulcz/deja-vu` deposunu yükler:

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

[`snacks.nvim`](https://github.com/folke/snacks.nvim), [`telescope.nvim`](https://github.com/nvim-telescope/telescope.nvim) (`:Telescope deja`), [`fzf-lua`](https://github.com/ibhagwan/fzf-lua) veya yerleşik `vim.ui.select` ile ek ayar olmadan çalışır.

## Komutlar ve Kısayollar

| Komut | Varsayılan kısayol (`keymaps = true`) | Çalıştırdığı işlem |
| --- | --- | --- |
| `:DejaRecall [query]` | `<leader>dr` | İndekslenmiş tüm oturumlarda tam metin araması (`deja search --json`); görsel seçim desteklenir |
| `:DejaLast [count]` | `<leader>dl` | Kurulu tüm ajanlardaki son oturumları listeler (`deja last --json`) |
| `:DejaBlame[!]` | `<leader>db` | İmleç satırını hangi AI kodlama oturumunun yazdığını gösterir (`deja blame file:line --json`; tüm dosya için `!`) |
| `:DejaHandoff [id]` | `<leader>dh` | Ajanlar arası devir özetini kopyalar ve açar (`deja handoff`) |
| `:DejaFix [error]` | `<leader>df` | Mevcut LSP tanı mesajını veya hatayı neyin çözdüğünü getirir (`deja fix`) |
| `:DejaShow <id>` | — | Gizli bilgileri ayıklanmış oturum kaydını yüzen Markdown penceresinde açar (`y` ID kopyalar, `H` devir, `R` bölünmüş pencerede devam) |

## Durum çubuğu (`lualine.nvim`)

```lua
sections = {
  lualine_x = {
    function() return require("deja").statusline() end,
  },
}
```

## CodeCompanion (`codecompanion.nvim`)

`vshulcz/deja-vu`, sohbet tamponuna devir özeti ekleyen `/deja` eğik çizgi komutunu ve `#{deja}` / `#{deja:query}` düzenleyici bağlamını kaydeden `lua/codecompanion/_extensions/deja/init.lua` dosyasını da içerir:

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

İkili tespitini ve etkin seçici arka ucunu doğrulamak için Neovim içinde `:checkhealth deja` çalıştırın.

## Ölçümler

| İsabet (Recall) | Token | İndeksleme ve sorgu |
|---|---|---|
| LongMemEval-S'te **%97.2 R@5** (470 soruda %88.9 hit@1) | **%58 daha az token** (tekrarlanan depo işlerinde) | 19,195 oturumu **13.6 sn**'de indeksler / 53 kodlama ajanı genelinde **0.7 ms** sorgu süresi |

## Depo belleği ve ajanlar arası devir (`handoff`)

Onaylanmış proje kararlarını ve çıkmaz yolları git üzerinden `.deja/memory.jsonl` dosyasında paylaşın (`deja repo candidates`, `deja repo add <id>`). Kayıtlar yazılırken gizli anahtarlardan arındırılır, paralel dalların temiz birleşmesi için `merge=union` ile içerik özeti sırasıyla tutulur ve depoyu klonlayan her ekipte yüklenir (güven politikasında `{"activations":{"auto":{"repo":false}}}` kanca enjeksiyonunu kapatır). [Ayrıntılar](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Görevin ortasında araç mı değiştiriyorsunuz? `deja handoff --to <agent>` çalıştırın (veya MCP üzerinden `handoff` çağırın); etkin oturumun hedefi, kararları, dokunulan dosyaları, geçen testleri ve açık işleri başka kodlama ajanına aktarılır.

[deja-vu](https://github.com/vshulcz/deja-vu) parçasıdır. MIT.
