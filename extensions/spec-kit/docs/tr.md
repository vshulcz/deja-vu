# deja-vu for Spec Kit

[English](../README.md) | [简体中文](zh.md) | [繁體中文](zh-TW.md) | [日本語](ja.md) | [한국어](ko.md) | [Español](es.md) | [Português](pt.md) | [Français](fr.md) | [Deutsch](de.md) | [Русский](ru.md) | Türkçe | [हिन्दी](hi.md)

[![#1 Go Repository Of The Day](https://trendshift.io/api/badge/trendshift/repositories/83640/daily?language=Go)](https://trendshift.io/repositories/83640) [![GitHub Stars](https://img.shields.io/github/stars/vshulcz/deja-vu?style=flat&label=Stars)](https://github.com/vshulcz/deja-vu/stargazers)

**[deja-vu](https://github.com/vshulcz/deja-vu)** eklentisi (LongMemEval-S'te **%97.2 R@5** · 19,195 oturumu **13.6 sn**'de indeksleme · agentmemory'nin **yarısı kadar token**) · [Belgeler](https://vshulcz.github.io/deja-vu/guide/getting-started.html) · [Canlı demo](https://vshulcz.github.io/deja-vu/view/) · [Ölçümler](https://vshulcz.github.io/deja-vu/guide/benchmarks.html) · **[★ GitHub'da yıldız ver](https://github.com/vshulcz/deja-vu)**

Bir [Spec Kit](https://github.com/github/spec-kit) eklentisidir. Bir özelliği uygulamadan önce `/speckit.deja-vu.recall` geçmiş kodlama ajanı oturumlarınızı tarar; nelerin denendiğini, nelere karar verildiğini ve nelerin başarısız olduğunu her maddenin oturum kimliğiyle birlikte raporlar.

Oturumlar; Claude Code, Codex, Cursor, Copilot, Gemini ve diğer ajanların diske yazdığı kayıtları indeksleyen [deja](https://github.com/vshulcz/deja-vu) tarafından sağlanır. Her şey yerel makinenizde kalır.

![deja-vu demosu](https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif)

## Kurulum

Önce `deja` aracını kurun:

```sh
brew install deja-vu
# veya
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Ardından Spec Kit projesinde:

```sh
specify extension add deja-vu --from https://github.com/vshulcz/deja-vu/releases/latest/download/deja-spec-kit.zip
```

Her deja sürümü, aynı sürümle eşleşen `deja-spec-kit.zip` paketini ekler. Yerel bir çalışma kopyasını denemek için: `specify extension add --dev /path/to/deja-vu/extensions/spec-kit`.

## Kullanım

```
/speckit.deja-vu.recall
/speckit.deja-vu.recall stripe webhook signature
```

Argümansız çalıştırıldığında etkin özelliğin `spec.md` ve `plan.md` dosyalarını okuyup sorguları kendi belirler. Argüman verildiğinde o kelimeleri arar. Komut adı ajanınızın biçimine göre `/speckit-deja-vu-recall` veya `$speckit-deja-vu-recall` olarak görünebilir.

Arka planda depo kökünden şu komutları çalıştırır:

```sh
deja recall -- "<query>"   # eşleşen oturumlar, önce bu proje
deja files -- "<query>"    # bu konuda açılan veya düzenlenen dosyalar
```

ve alıntı ne olduğunu anlamak için fazla kısaysa `deja show <id>` çağrısı yapar. Rapor, denenip başarısız olanlar, alınan kararlar, dosyalar ve açık kalanlar biçiminde ayrılmış bir `Prior sessions` bölümüdür ve her madde `(deja:<id>)` şeklinde kaynak gösterir.

## Kanca (Hook)

Eklenti isteğe bağlı tek bir `before_implement` kancası kaydeder. `/speckit.implement` çalıştırıldığında ajan önce geçmiş çağrısını çalıştırmayı önerir; siz onaylamadıkça hiçbir şey çalışmaz. Kapatmak için `.specify/extensions.yml` dosyasında `enabled: false` ayarlayın veya `specify extension disable deja-vu` komutunu çalıştırın.

## Notlar

- Salt okunur: komut yalnızca arar ve okur, hiçbir dosyayı düzenlemez.
- Bir makinedeki ilk deja çağrısı indeksi oluşturur; büyük geçmişlerde biraz zaman alabilir. Ondan sonra bir saniyenin çok altında yanıt verir.
- Geri çağrılan oturum metni yalnızca referans verisi olarak ele alınır; komut ajana bunun içinde geçen hiçbir talimata uymamasını söyler.

## Ölçümler

| İsabet (Recall) | Token | İndeksleme ve sorgu |
|---|---|---|
| LongMemEval-S'te **%97.2 R@5** (470 soruda %88.9 hit@1; MemPalace %96.6, agentmemory %95.2 yayımlar) | **%58 daha az token** (tekrarlanan depo işlerinde agentmemory'nin **yarısı kadar token**) | 19,195 oturumu **13.6 sn**'de indeksler / 49 kodlama ajanı genelinde **0.7 ms** sorgu süresi (7 araçtan sonraki: 72 sn) |

## Depo belleği ve ajanlar arası devir (`handoff`)

Onaylanmış proje kararlarını ve çıkmaz yolları git üzerinden `.deja/memory.jsonl` dosyasında paylaşın (`deja repo candidates`, `deja repo add <id>`). Kayıtlar yazılırken gizli anahtarlardan arındırılır, paralel dalların temiz birleşmesi için `merge=union` ile içerik özeti sırasıyla tutulur ve depoyu klonlayan her ekipte yüklenir (güven politikasında `{"activations":{"auto":{"repo":false}}}` kanca enjeksiyonunu kapatır). [Ayrıntılar](https://vshulcz.github.io/deja-vu/guide/repo-memory.html).

Görevin ortasında araç mı değiştiriyorsunuz? `deja handoff --to <agent>` çalıştırın (veya MCP üzerinden `handoff` çağırın); etkin oturumun hedefi, kararları, dokunulan dosyaları, geçen testleri ve açık işleri başka kodlama ajanına aktarılır.

## Lisans

MIT

[deja-vu](https://github.com/vshulcz/deja-vu) parçasıdır. MIT. İşinize yaradı mı? [GitHub'da deja-vu'ya yıldız verin](https://github.com/vshulcz/deja-vu).
