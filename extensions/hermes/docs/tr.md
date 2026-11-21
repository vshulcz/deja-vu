# deja-memory for Hermes

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | Türkçe | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640)

**[deja-vu](https://github.com/vshulcz/deja-vu)** eklentisi (LongMemEval-S'te **%97.2 R@5**) · [Belgeler](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Canlı demo](https://vshulcz.github.io/deja-vu/view/) · [Ölçümler](https://vshulcz.github.io/deja-vu/guide/benchmarks.html)

Bu makinede zaten bulunan kodlama oturumlarından yanıt veren bir Hermes bellek sağlayıcısıdır: Claude Code, Codex, Cursor, Hermes'in kendisi ve deja'nın okuduğu diğer ajanlar (herhangi bir şey kurulmadan önceki aylar dahil). Ek model yok, API anahtarı yok, arka plan sunucusu yok; geçmiş, tek bir Go ikilisi olan [deja](https://github.com/vshulcz/deja-vu) tarafından yerel olarak indekslenir.

![deja-vu demosu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Kurulum

Önce `deja` komutunun `PATH` üzerinde olması gerekir:

```bash
brew install deja-vu        # veya: curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto         # Hermes'i ve bu makinedeki desteklenen tüm ajanları bağlar
hermes plugins install deja-memory   # veya GitHub'dan: hermes plugins install vshulcz/deja-vu/extensions/hermes
hermes config set memory.provider deja-memory
```

ya da `hermes memory setup` içinden `deja-memory` seçeneğini belirleyin; etkinleştirmeden önce ikiliyi kontrol eder.

## Ne yapar

- **Oturumun ilk turundan önce**: deja kancalarının Claude Code ve Codex'e verdiği proje özetini (bu projedeki son çalışmalar ve kesinleşen kararlar) Hermes'e iletir. Bundan sonra yalnızca o an sorulan şey geçmişle eşleşirse ekleme yapar; turların çoğunda hiçbir şey eklemez.
- **Neyi getirdiğini gösterir**: deja'nın Claude Code'da gösterdiği satırlar (hangi ajandan geldiğini belirten makbuz, oturum başı hatırlatması, ilk derleme ve haftalık notlar) etkileşimli CLI ve `hermes chat -q` içinde Hermes'in `🧠 deja · …` satırından geçer. Modelin metni değişmez. Hermes 0.17'de bu satır yoktur, `hermes -z` ve `hermes chat -Q` yazdırmaz, TUI yalnızca durum çubuğunda kısaca gösterir ve ağ geçidi sohbeti başkalarının da okuduğu bir grup olabilir; bu durumlarda deja notlarını gösterebilen bir oturuma saklar.
- **Zamanlanmış (`cron`) işlerde otomatik hatırlatma yok**: Hermes platformu `cron` olarak bildirdiğinde sağlayıcı kendiliğinden bir şey eklemez; iş çağırdığında deja araçları yine yanıt verir.
- **Üç araç**: `deja_recall` geçmiş oturumları arar, `deja_fix` bir hatadan sonra geçen sefer ne çalıştırıldığını söyler, `deja_blame` bir dosyayı düzenleyen oturumları listeler.
- **Not önceliği**: Hermes'in `MEMORY.md` veya `USER.md` içine eklediği girdiler deja notu olarak da tutulur ve çevresindeki oturumlardan yukarıda sıralanır.
- **Sıkıştırma koruması**: Sıkıştırmada (compaction) oturumun neyin ortasında kaldığını bir sonraki tur için saklar ve oturum bittiğinde canlı damgasını kaldırır.

Bir bellek sağlayıcısının tek başına yapamadığı bir şey var: az önce başarısız olan komutun yanına önceki düzeltmeyi koymak. Bu işlem `transform_tool_result` kancasına dayanır; ancak Hermes bellek sağlayıcısına `register_hook` çağrısı etkisiz bir bağlam verir (`plugins/memory/__init__.py`, 0.17.0). `deja install hermes-auto` bunu taşıyan kanca eklentisini de yazar.

Hermes turlarını ayrıca bir yere yazmaz: Hermes onları zaten kaydeder ve deja bir sonraki yenilemede o depodan okur.

## Ölçümler

| İsabet (Recall) | Token | İndeksleme ve sorgu |
|---|---|---|
| LongMemEval-S'te **%97.2 R@5** (470 soruda %88.9 hit@1) | **%58 daha az token** (tekrarlanan depo işlerinde) | 19,195 oturumu **13.6 sn**'de indeksler / 52 kodlama ajanı genelinde **0.7 ms** sorgu süresi |

## Depo belleği ve ajanlar arası devir (`handoff`)

Onaylanmış proje kararlarını ve çıkmaz yolları git üzerinden `.deja/memory.jsonl` dosyasında paylaşın (`deja repo candidates`, `deja repo add <id>`). Kayıtlar yazılırken gizli anahtarlardan arındırılır, paralel dalların temiz birleşmesi için `merge=union` ile içerik özeti sırasıyla tutulur ve depoyu klonlayan her ekipte yüklenir (güven politikasında `{"activations":{"auto":{"repo":false}}}` kanca enjeksiyonunu kapatır). [Ayrıntılar](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Görevin ortasında araç mı değiştiriyorsunuz? `deja handoff --to <agent>` çalıştırın (veya MCP üzerinden `handoff` çağırın); etkin oturumun hedefi, kararları, dokunulan dosyaları, geçen testleri ve açık işleri başka kodlama ajanına aktarılır.

## `deja install` ile aynı kod

`deja install hermes-auto` bu sağlayıcıyı `~/.hermes/plugins/deja-memory` içine MCP sunucusu ve `pre_llm_call` kanca eklentisiyle birlikte yazar. Buradaki dosyalar aynı kaynaktan (`cmd/deja/install_hermes_memory.go`) üretilir; tek fark bu kopyanın yolu içine yazmak yerine `PATH` üzerinde `deja` aramasıdır. İkisi ayrışırsa `TestHermesExtensionIsTheProviderInstallWrites` testi hata verir.

[deja-vu](https://github.com/vshulcz/deja-vu) parçasıdır. MIT.
