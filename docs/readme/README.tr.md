<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo-dark.svg">
    <img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/logo.svg" width="330" alt="deja-vu">
  </picture>
</p>

<p align="center"><b>Kodlama ajanlarınız, zaten düzelttiğiniz şeyi yeniden hata ayıklamaz.</b></p>

<p align="center">Claude Code, Codex, Cursor ve bu makinedeki diğer ajanlar her oturumu zaten diske kaydediyor.
deja aylar öncesi dahil hepsini indeksler ve önemli kısmı
şu an çalışan ajana verir.</p>

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/demo.gif" width="720" alt="aynı soru aynı ajana iki kez: bellek yokken hiçbir şey hatırlamıyor, deja varken sekiz ay önceki sonuçla yanıtlıyor"></p>

<p align="center"><sub><em>Kimse arama yapmadı; ajan deja'yı kendisi çağırdı. Gerçek model ve gerçek araç çağrılarıyla iki gerçek çalıştırma, sentetik bir külliyat üzerinde: kimsenin geçmişi yayımlanmıyor.</em></sub></p>

<p align="center"><b>deja ilk dakikadan itibaren dolu: 41 ajanın çoktan yazdığı geçmiş, saniyeler içinde indekslenir, model de ayrı bir toplama adımı da gerekmez.</b></p>

<table align="center">
<tr>
<td align="center" width="33%">İsabet<br>LongMemEval-S'te <b>%97.2 R@5</b><br><sub>500 sorunun tamamı · MemPalace %96.6, agentmemory %95.2<br>aynı 19,195 oturumda: 100 sorudan 19'u ilk sırada, en yakını 14</sub></td>
<td align="center" width="33%">Token<br>agentmemory'nin <b>yarısı kadar token</b><br><sub>zaten çözülmüş bir görevde 104,974'e karşı 53,558<br>tur başına 477 token araç tanımı, yedi sunucunun en azı</sub></td>
<td align="center" width="33%">İndeksleme<br>19,195 oturumu <b>17.6 sn</b>'de indeksler<br><sub>yedi araçtan sonraki: 72 sn<br>ilk yanıta 26 ms, arama medyanı 97 ms</sub></td>
</tr>
</table>

<p align="center"><sub>deja-vu'yu ben geliştiriyorum; bu yüzden tüm sürücüler, derlem ve puanlama kuralı bu depoda &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/day-zero.html">birebir karşılaştırma ve şüphelendiğiniz bir satırı yeniden ölçme</a> &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">LongMemEval ve LoCoMo çalıştırmaları</a></sub></p>

<p align="center"><a href="../../README.md">English</a> | <a href="README.zh.md">简体中文</a> | <a href="README.zh-TW.md">繁體中文</a> | <a href="README.ja.md">日本語</a> | <a href="README.ko.md">한국어</a> | <a href="README.es.md">Español</a> | <a href="README.pt.md">Português</a> | <a href="README.fr.md">Français</a> | <a href="README.de.md">Deutsch</a> | <a href="README.ru.md">Русский</a> | Türkçe | <a href="README.hi.md">हिन्दी</a></p>

<p align="center"><a href="https://vshulcz.github.io/deja-vu/">Belgeler</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">Ölçümler</a> &middot; <a href="https://vshulcz.github.io/deja-vu/guide/compare.html">Karşılaştırma</a></p>
<p align="center"><sub>İşinize yaradıysa <a href="https://github.com/vshulcz/deja-vu">GitHub</a>'da deja-vu'ya bir yıldız bırakın.</sub></p>

## Kurulum

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

`raw.githubusercontent.com` erişilemiyorsa aynı sürümü sunan bir npm aynası var:

```sh
npm i -g @vshulcz/deja-vu --registry=https://registry.npmmirror.com
deja install --auto
```

Kurulum on saniye, indeksleme on saniye kadar. İkinci komut bulduğu her ajana MCP geri çağırmayı bağlar,
ajanın desteklediği yerlerde oturum başlangıcında geri çağırmayı açar ve ilk indeksi kurar; böylece bir
sonraki oturum hiçbir şey beklemez.

Yeni bir ajan oturumu açın ve aylar önce yaptığınız bir şeyi sorun:

> daha önce jwt refresh rotation ile uğraşmış mıydık? belleğine bak

Sormanız da gerekmiyor: otomatik geri çağırma açıkken ajan, oturum açılır açılmaz bu projede neyin çözüldüğünü
zaten biliyor. Kendiniz bakmak için `deja` çalıştırın.

opencode, DeepSeek Harness, Zed, Kimi Code, Codex CLI, Grok Build, OpenClaw, pi ve Hermes için kendi ekosistemlerinde
birer paket de var; eklentilerini oradan kurmaya alışkın olanlar için:

```sh
opencode plugin opencode-deja
dsh plugin --profile web add dsh-deja
# Zed: eklenti panelinde deja'yı arayın
# Kimi Code: /plugins install https://github.com/vshulcz/deja-vu
# Codex CLI: codex plugin marketplace add https://github.com/vshulcz/deja-vu && codex plugin add deja-vu@deja-vu
# Grok Build: grok plugin marketplace add xai-org/plugin-marketplace && grok plugin install deja
openclaw plugins install clawhub:@vshulcz/openclaw-deja && openclaw config set plugins.entries.deja-vu.hooks.allowConversationAccess true
pi install npm:@vshulcz/pi-deja
hermes plugins install deja-vu
```

`deja install --auto` yukarıdakilerin hepsini zaten bağlıyor, yani iki yoldan biri yeter. İkisi birden de
sorun çıkarmaz: her paket `deja install`'ın yazdığını okur ve yalnızca eksik olanı tamamlar; araçlar iki kez
kaydedilmez, geri çağırma iki kez çalışmaz. Ayrıntılar [`extensions/`](../../extensions) altında.

Aynı arama bir skill olarak da var; `SKILL.md` okuyan her ajan kurabilir:

```sh
npx skills add https://github.com/vshulcz/deja-vu --skill deja-search   # skills CLI: Claude Code, Cursor, Goose, Copilot…
openclaw skills install @vshulcz/deja-search                            # ClawHub
hermes skills install vshulcz/deja-vu/skills/deja-search                # Hermes
```

Skill, kurduğunuz `deja` ikilisini çağırır; kendi kopyasını taşımaz.

Diğer yollar: `brew install deja-vu`,
`go install github.com/vshulcz/deja-vu/cmd/deja@latest` ya da hiçbir şey kurmadan denemek için
`npx @vshulcz/deja-vu "sorgu"`. Windows'ta kurulum betiği `unsupported OS` diyerek çıkar, çünkü bir shell
betiği; `scoop install deja-vu` kullanın (Scoop'un main bucket'ında) veya `winget install vshulcz.deja-vu` kullanın ya da [son sürümden](https://github.com/vshulcz/deja-vu/releases/latest)
`deja-vu_<version>_windows_amd64.zip` dosyasını alıp `deja.exe`'yi `PATH`'e koyun.

Yalnızca ikili dosya bile tam bir kurulumdur: indeksleme, arama, `show`, `ctx`, `blame`, `--json` ve kimlik
bilgisi temizliği başka hiçbir şeye ihtiyaç duymaz. `deja install`'ın yaptığı, MCP'yi ajanlarınıza bağlamak ve
oturum başlangıcında geri çağırmayı açmaktır; değerli ama isteğe bağlı.

## Kendiniz arayın

Terminalde argümansız `deja`, tüm ajanların geçmişinde bir arama ekranı açar. Önce commit edilmemiş
değişikliğinizin arkasındaki oturumları, sonra bu projedeki son oturumları gösterir; siz yazdıkça liste
daralır.

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/assets/tui.gif" width="720" alt="deja ekranında bir arama yazmak, bulunan oturumu okumak ve o oturuma başka bir ajanda devam etmek"></p>

- `↑↓` bir oturum seçer, `↵` onu eşleşmenin olduğu yerde açar, `n` ve `N` eşleşmeler arasında gezer, `esc` geri döner.
- `r` oturumu onu yazan ajanda sürdürür. `o` başka herhangi bir ajanda devam ettirir; o ajan, oturumun ne sorduğu, neye karar verdiği ve neyi açık bıraktığıyla başlar.
- `tab` bu proje, tüm projeler ve Kept arasında geçiş yapar; Kept, ajanının sildiği ama deja'nın hâlâ tuttuğu oturumlardır. `R` birini yerine koyar.
- `?` tüm tuşları listeler, `q` çıkar.

`DEJA_TUI=0` ile bunun yerine düz metin özeti yazdırır; bu, `deja brief` çıktısıyla aynıdır. Bir pipe'a
bağlıyken neyin indekslendiğini ve sırada hangi komutun çalıştırılacağını yazdırır.

## Ne kazandırıyor

**Codex'te çözüldü, Claude hatırlıyor.** Kırk bir kodlama ajanı her konuşmayı yerel dosyalara yazıyor; deja
bu dosyaları hepsinin okuyabildiği bir bellek katmanına dönüştürüyor.

| | |
| --- | --- |
| **Geriye doğru arama** | `deja "connection pool exhausted"` gigabaytları tarar, deja'yı kurmanızdan öncesi dahil. Doğal dildeki bir soru ilgi düzeyine göre çalışan kipe düşer. Zaman bir ipucudur, filtre değil. |
| **Ajanlar arası geri çağırma** | MCP'deki `deja` aracı `recall` kipinde, hangi ajanın içinden olursa olsun "bunu üç hafta önce düzeltmiştik" yanıtını verir; o zaman kimin düzelttiği fark etmez. |
| **Sıkıştırmadan sağ çıkar** | 43 bağlam sıkıştırması üzerinde ölçüldü: özet, kararların %77'sini ve çalıştırdığınız komutların %0.2'sini korudu. Kalan %99.8'i deja geri verir. Claude Code ve Codex'te deja, sıkıştırma başladığı anda görevi, dosyaları ve komutları not eder ve sıkıştırmadan sonra bir kez geri verir. |
| **Harekete geçme anında geri çağırma** | Ajan bir dosyayı değiştirmeden veya bir komut çalıştırmadan önce `PreToolUse` kancası, o dosya hakkında daha önce ne karar verildiğini, o komutun burada işe yarayan biçimini ya da programın bu makinede hiç olmadığını söyler. Komut başarısız olduğunda `PostToolUse` kancası, bu makinede aynı hatadan sonra ne çalıştırıldığını gösterir — ajanın kendiliğinden sormayacağı tam da bu ikilidir. |
| **Konuşmayı değil işi indeksler** | Her turda açılan her dosya, çalıştırılan her komut ve çıkış kodu, bir düzenlemenin yerine geçtiği tam parça. Her özetin kaybettiği şey tam olarak budur. |

Ayrıca: `deja promote <id> --state rejected` geri alınmış bir kararı işaretler ve bundan sonra her sonuç onun
denenip elendiğini gösterir; bir sonuç "bu oturumun dokunduğu 4 dosya o zamandan beri değişti" der ve karar
veremediğinde susar; `deja sync ssh laptop` belleği makineler arasında yalnızca ekleyerek taşır, araya bulut
girmez; `deja handoff --to codex` başka bir ajanda devam etmek için mevcut bağlamı paketler; bilinen biçimdeki
anahtarlar, token'lar, JWT'ler ve özel anahtar blokları indeksleme sırasında çıkarılır — ne var ki örüntü
eşleştirme sır tespiti değildir ve bilinmeyen bir biçim geçebilir.

### Kendi çalışmanızı çizin

`deja stats --card` doğrudan terminale çizer; bir dosya adı verirseniz profil README'niz için bir SVG yazar.
Başka bir yerde yayımlamak için [PNG'ye çevirin](https://vshulcz.github.io/deja-vu/card/) — o sayfa dönüşümü
sizin tarayıcınızda yapar.

<p align="center"><img src="https://raw.githubusercontent.com/vshulcz/deja-vu/main/docs/assets/stats-card-demo.svg" width="760" alt="deja istatistik kartı: bir yıllık oturumlar ısı haritası olarak, hangi ajanlardan geldikleri ve en uzun olanı"></p>

Tam başvuru [belge sitesinde](https://vshulcz.github.io/deja-vu/).

## Gizlilik

İndeksleme ve arama yereldir. Ağı yalnızca `deja update`, `deja sync ssh`, `deja doctor` içindeki sürüm
denetimi, sizin yapılandırdığınız uç noktaya giden `deja embed` ve etkileşimli bir komutun günde bir kez son
sürüme bakması kullanır. Bu bakış oturum verisi göndermez; `DEJA_OFFLINE=1` ya da `DEJA_NO_UPDATE_NOTICE=1`
onu kapatır.

Kimlik bilgileri indeksleme sırasında temizlenir: AWS anahtarları, `api_key=` ve `token=` atamaları, bearer
token'lar ve çıplak JWT'ler, PEM özel anahtar blokları, çeşitli sağlayıcıların token'ları,
`scheme://user:pass@host` biçimindeki URL'ler, hiçbir kuralın kapsamadığı yüksek entropili değerler ve düz
metin içine yazılmış parolalar — "the admin password is …" gibi, tutunacak hiçbir ayracın olmadığı durumlar.
Değer `[redacted:<kind>]` olur, çevresindeki metin aranabilir kalır. `deja share` ve `deja sync export` dışa
aktarırken bir kez daha temizler. Örüntü eşleştirme sır tespiti değildir: kuralların tanımadığı bir biçim
olduğu gibi geçebilir, güvenlik modeline bakın.

`deja forget` oturumları yeniden kurulan indeksten çıkarır ve bir mezar taşı bırakır; böylece sonraki
`deja index` onları özgün geçmişten geri getiremez.
[Güvenlik modeli](../../docs/SECURITY-MODEL.md) veri akışlarını, temizliğin sınırlarını, güven varsayımlarını ve
sürüm doğrulamasını belgeler.

## Komut satırı

Terminalde yalın `deja` [arama ekranını](#kendiniz-arayın) açar. Bir sorguyla ya da aşağıdaki komutlardan
biriyle çalıştırıldığında çıktıyı yazdırıp çıkar:

```text
$ deja "jwt refresh token"
[claude] api        · Jul 8 · 8f31c0a9 — 2 matches
  login started failing after refresh token rotation; jwt kid mismatch in tests
  fixed by reloading jwks cache after rotateKey and adding a clock-skew test
[codex]  web        · Jul 1 · b77d91e2 — 1 match
  refresh token cookie needed SameSite=Lax in local callback flow
```

| Komut | Ne yapar |
| --- | --- |
| `deja <sorgu>` | Tüm geçmişte arar. Birden çok sözcük VE anlamına gelir, tırnak bitişik metin ister; tam eşleşme yoksa sözcük biçimlerini ve yakın yazımları dener. |
| `deja` | Terminalde, yukarıdaki tam ekran arama. `deja brief` bunun yerine metin özetini yazdırır: bugünün oturumları, deja'nın neler sunduğu, son çalışmalar ve denenecek bir arama. |
| `deja wip` | Bu dizindeki son oturum ne yapıyordu: görev, varılan sonuç, elde olan dosyalar, son komut ve başarısız olup olmadığı. Hepsi kayıtlardan çıkarılır, kimsenin not tutmuş olmasına bağlı değildir. |
| `deja blame <yol>[:satır]` | Bu dosyayı hangi oturumlar konuştu, ne karar verildi ve neden. Satır numarası verilirse: o satırı en son değiştiren commit ve o satırı ya da o commit'in değiştirdiği metni yazan oturum. |
| `deja files <konu>` | Ters yön: bir konudaki çalışmanın gerçekte hangi dosyalara dokunduğu. |
| `deja how <araç>` | Bu makinede bu iş gerçekte nasıl çalıştırılıyor — ajanların daha önce çalıştırdığı komutlardan alınmış gerçek argümanlarla. |
| `deja fix <hata>` | Bu makinede aynı hatadan sonra ne çalıştırıldı ve ondan sonra hata bir daha çıkmadı. Asla bir merge, force push ya da silme değil. |
| `deja friction` | Üç veya daha fazla farklı oturuma isabet eden hatalar ve hangi araçlardan geldikleri. |
| `deja ctx <sorgu>` | En iyi sonucun Markdown özeti, doğrudan bir prompt'a girecek biçimde. |
| `deja resume <id>` | Bulunan oturumu ait olduğu araçta yeniden açar. |
| `deja view` | Tüm belleği tek bir yerel HTML dosyasına aktarır. Sunucu yok, veri makineden çıkmaz. |
| `deja doctor [--deep]` | Kendi kendine denetim; `--deep` ile indeksi kaynak dosyalara karşı doğrular. |
| `deja mcp` | stdio üzerinden MCP sunucusu — `deja install`'ın bağladığının ta kendisi. |

Tam başvuru [komut belgelerinde](https://vshulcz.github.io/deja-vu/guide/commands.html).

### MCP araçları

Sunucu tek bir `deja` aracı sunar, yeteneği `mode` parametresi seçer: `recall`, `context`, `blame`, `fix`,
`how`, `orient`, `remember`, `handoff`. `deja install` bunu kendiliğinden bağlar, yani yalnızca bir ajanı elle yapılandırırken
önemlidir. Eski altı araç adı, hâlihazırda bağlı istemcilerde çalışmaya devam eder.

Yedi yerine tek araç bir maliyet meselesidir, üslup meselesi değil. Bağlı bir MCP sunucusu araç tanımlarını
her istekle birlikte gönderir, dolayısıyla ajan bir şey çağırsa da çağırmasa da her turda bedeli ödenir:
burada 477 token, ölçülen yedi sunucunun en büyüğünde ise 8,283. deja'nınki de, şema kipleri olan tek bir
araca indirilene kadar 828'di.

## Desteklenen araçlar

Otomatik geri çağırma açıkken Claude Code ve Codex, sıkıştırma başladığında mevcut kaydı deja'ya verir; deja da
özetin birazdan kaybedeceğini saklar: görevi, sonuçları, dosyaları, her komutun nasıl bittiğini ve geriye ne
kaldığını. Aynı oturumdaki ve aynı çalışma dizinindeki sonraki kanca bunu tek seferde, 4 KB'ı aşmadan geri
verir ve deponun o zamandan beri değişip değişmediğini bir satırla ekler. `deja stats`, sıkıştırma ile ilk
düzenleme arasındaki araç çağrısı sayısını sayar; bu özellik onunla ölçüldü.
Ayrıntılar [sıkıştırma sonrası kurtarma](../../docs/compaction.md) belgesinde.

Claude Code · Cline · Codex CLI · opencode · aider · Gemini CLI · Cursor · Antigravity ·
Grok Build · Hermes · Goose · Qwen Code · Kimi Code · pi · omp (Oh My Pi) · OpenClaw ·
Copilot CLI · VS Code Copilot Chat · Amp · prime-agent (PrimeIntellect) · Roo Code ·
Continue · Crush · DeepSeek Harness · Cherry Studio · Senpi · gajae-code · Kimchi Coding ·
Command Code · ZCode · CodeWhale · Reasonix · Kiro · Kilo Code · CodeBuddy Code ·
TRAE CLI · Muse Code · Junie · JetBrains AI Assistant · Devin CLI · Zed.

Her birinin neyi desteklediği — MCP geri çağırma, otomatik geri çağırma, skill'ler, komutlar, resume, handoff —
[İngilizce README'deki yetenek tablosunda](../../README.md#supported-harnesses). Özel depolama konumları `DEJA_*_ROOT`
değişkenleriyle belirtilir; araçların kendi geçiş değişkenlerine de uyulur.

### Kendi paketi olan ajanlar

`deja install --auto` bunları da diğerleri gibi bağlar ve bu her zaman en kısa yoldur. Ayrıca her birinin
kendi ekosisteminde bir paketi var; eklentilerini oradan kuranlar için:

| Ajan | Paket | Kurulum |
| --- | --- | --- |
| opencode | npm `opencode-deja` | `opencode plugin opencode-deja` |
| DeepSeek Harness | npm `dsh-deja` | `dsh plugin --profile web add dsh-deja` |
| Zed | `deja-context-server` | Zed → Extensions → deja |
| Kimi Code | eklenti `deja` | `/plugins install https://github.com/vshulcz/deja-vu` |
| Codex CLI | eklenti `deja-vu` | `codex plugin marketplace add https://github.com/vshulcz/deja-vu`, sonra `codex plugin add deja-vu@deja-vu` |
| Grok Build | eklenti `deja` | `grok plugin marketplace add xai-org/plugin-marketplace`, sonra `grok plugin install deja` |
| OpenClaw | ClawHub ve npm `@vshulcz/openclaw-deja` | `openclaw plugins install clawhub:@vshulcz/openclaw-deja` |
| pi (ve omp) | npm `@vshulcz/pi-deja` | `pi install npm:@vshulcz/pi-deja` |
| Hermes | Hermes eklenti kataloğunda `deja-memory` bellek sağlayıcısı | `hermes plugins install deja-vu` ya da GitHub'dan: `hermes plugins install vshulcz/deja-vu/extensions/hermes` |

İki yoldan biri yeter, ikisi birden de bir şey bozmaz: her paket önce `deja install`'ın yazdığını okur.
opencode, dsh ve OpenClaw yalnızca eksiği tamamlar; Kimi, Grok, Codex ve pi kurucu zaten bağladıysa geri
çekilir; Zed'de iki taraf aynı sunucu kimliğini kullanır. Yani kurulum sırası önemli değil.

Hepsi sizin kurduğunuz deja'yı kullanır; paketin içindeki kopya yalnızca yedektir.

## İsteğe bağlı anlamsal geri çağırma

`DEJA_EMBED_URL` ile `deja embed`'i yerel bir Ollama'ya, LM Studio'ya veya OpenAI uyumlu herhangi bir uç
noktaya yönlendirin; başka sözcüklerle sorulan soru da isabet etsin. Kullanılabilir bir çalışma zamanı yoksa
sözcüksel arama ve MCP geri çağırma her zamanki gibi çalışır.

## Kanıt

<p>
Bu makinenin daha önce çözdüğü bir işte <b>%58 daha az token</b> &middot; LongMemEval-S üzerinde (470 soruluk temizlenmiş küme) <b>%88.1 hit@1</b> &middot; LoCoMo üzerinde <b>%70.5</b> &middot; gigabaytlarca geçmişte <b>milisaniyelik</b> sorgular<br>
<sub>Kol başına on bir çalıştırma: hiçbir şey bağlı değilken 126,222'ye karşı 53,558 token; sonraki bir sürümde yine on birer çalıştırma: 103,443'e karşı 52,815 &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/day-zero.html">bir işi bitirmenin maliyeti</a> &middot;
her iki erişim değerlendirme düzeneği de bu depoda ve açık veri kümelerinde dakikalar içinde koşuyor &middot;
<a href="https://vshulcz.github.io/deja-vu/guide/benchmarks.html">sayıları kendiniz doğrulayın</a></sub>
</p>

```sh
deja bench recall     # sıralama gerilemesi için alt sınır: 100 sorgu, yarısı Rusça, geri çağırma düşerse CI kırılır
deja bench context    # tohumlu 30 görev zinciri ve beş olumsuz kontrol
deja bench block      # teslim edilen metin parçasında yanıt hâlâ var mı
deja bench prompt     # prompt başına kanca ne zaman konuşuyor, ne zaman yanlış konuşuyor
deja bench ingest     # bir indeks güncellemesinin maliyeti: değişiklik yok, bir tur eklendi, yeni bir kayıt dosyası, dosyanın tümden yeniden yazılması
deja bench read       # veritabanı tabanlı bir deponun okunma maliyeti ve tek bir uzun değerin buna etkisi
```

Bağlam deneyi, deja'nın geri çağırmasını tam geçmişle, naif grep ile ve soğuk başlangıçla karşılaştırır.
Varsayılan tohumda:

| Yaklaşım | Token (ortanca) | Kapsam (ortanca) | Olumsuz kontrol token'ı |
| --- | ---: | ---: | ---: |
| deja-recall | 1,096 | 1.00 | 0 |
| full-history | 80,547 | 1.00 | 78,145 |
| naive-grep | 273,238 | 1.00 | 0 |
| cold | 0 | 0.00 | 0 |

Ham günlüklerde grep yapmakla aynı olgu kapsamı, yaklaşık 250 kat daha az token'la; tümünü yeniden oynatarak
bulunan oturumlardan yaklaşık 70 kat az; ve ilgili geçmişi olmayan zincirlere hiçbir şey enjekte edilmiyor.
Külliyat üreteci ve ilgililik etiketlemesi sıradan, okunabilir Go kodudur. Herhangi bir sayıya inanmadan önce
orada "ilgili"nin nasıl tanımlandığına bakın — bizimkiler dahil.

Gerçek bir depoda ölçüldü: 2,419 oturum, 179k mesaj, 1.9 GB kayıt.

| Ölçüt | Sonuç |
| --- | --- |
| Süreç içi sorgu | ortanca **0.7–0.8 ms**, LongMemEval-S samanlıklarında yaklaşık 15 ms |
| Uçtan uca `deja <sorgu>` | o depoda ortanca yaklaşık 0.2 s: süreç başlatma, tüm depoların tazelik denetimi, sıralama, yazdırma |
| Yalnızca tazelik denetimi | hiçbir şey değişmediğinde yaklaşık 50 ms |
| İndeks boyutu | 200 MB, külliyatın yaklaşık %10'u |

İndeks artımlıdır. Bir oturum dosyası uzadığında yalnızca o dosya yeniden okunur.

## Nasıl çalışır

`~/.cache/deja` içinde yerel bir ters indeks: JSONL ve SQLite depolarını ayrıştırır, kimlik bilgilerini
temizler, `records.bin` ile sözcük kovalarını yazar ve her dosyanın durumunu `manifest.gob` içinde tutar;
böylece ikinci çalıştırma yalnızca değişeni alır. MCP sunucusu, istatistikler, share ve sync aynı indeksi
okur. Ayrıntılar [docs/ARCHITECTURE.md](../../docs/ARCHITECTURE.md) dosyasında.

## Sık sorulanlar

<details>
<summary><b>Makinemden bir şey çıkıyor mu?</b></summary>

Siz istemedikçe hayır.
[Veri akışları](../../docs/SECURITY-MODEL.md#data-flows) bölümüne bakın.

</details>

<details>
<summary><b>Günlüklerde zaten duran sırlar ne olacak?</b></summary>

Onlar özgün aracın dosyalarında kalır, sizin ajanınızın
verisidir; `deja secrets` onları taşıyan oturumları adlarıyla verir ki anahtarları yenileyip silebilesiniz,
`--scrub` da erişebildiği kayıtları yeniden yazar. Bilinen biçimler indeksleme sırasında çıkarılır ve
özetlere, share'e ya da sync dışa aktarımına ulaşmaz. Örüntü eşleştirme sır tespiti değildir: bilinmeyen bir
biçim geçebilir. [Güvenlik modeline](../../docs/SECURITY-MODEL.md#redaction-boundary) bakın.

</details>

<details>
<summary><b>Ajanımı yavaşlatır mı?</b></summary>

Bir geri çağırma, yerel indekse yapılan sözcüksel bir sorgudur: ortanca 0.7–0.8 ms
ve hiçbir şey model beklemez. Kanca, süreç başlatmayı ve depoların tazelik denetimini ekler; birkaç gigabaytlık
bir depoda onlarca milisaniye.

</details>

<details>
<summary><b>Çalışma şeklimi değiştirmem gerekir mi?</b></summary>

Hayır. Geri çağırmayı ajanın kendisi çağırır; otomatik geri çağırma
açıkken oturum açılır açılmaz bu projede daha önce nelerin kararlaştırıldığını zaten bilir.

</details>

<details>
<summary><b>Diğer bellek araçlarından farkı ne?</b></summary>

| | deja | Bellek platformları<br>(Mem0, Letta, memU) | Oturum araması<br>(cass) |
| --- | :-: | :-: | :-: |
| Kurulmadan önceki işi bilir | evet | hayır | evet |
| Bir toplama adımı gerekir | hayır, dökümün kendisi bellektir | olguları ajan ya da kodunuz yazar | hayır |
| LLM veya gömme anahtarı gerekir | hayır | evet | isteğe bağlı |
| Sorulmadan geri çağırır | oturum başlangıcında ve araç çalıştırılmadan önce | hayır | hayır |

[Tam karşılaştırma](https://vshulcz.github.io/deja-vu/guide/compare.html) bunlardan on beş tanesini kapsıyor.

</details>

<details>
<summary><b>Claude Code oturum geçmişi nerede ve aranabilir mi?</b></summary>

`~/.claude/projects` altında, oturum başına bir JSONL
dosyası; Codex `~/.codex/sessions` altında, Cursor ise SQLite `state.vscdb` içinde. deja bunları
yerinde okur: yalın `deja` onları aramak ve okumak için bir ekran açar, `deja search` aynısını bir betikten yapar, `deja last` tüm ajanların son oturumlarını listeler, `deja view` ise tüm geçmişi yerel bir sayfa
olarak açar. Ajan başına yollar
[oturumların saklandığı yer](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html)
sayfasında.

</details>

<details>
<summary><b>Claude Code geçmişim kayboldu, gitti mi?</b></summary>

Claude Code 30 günden eski kayıtları siler
(`~/.claude/settings.json` içindeki `cleanupPeriodDays`) ve `claude --resume` yalnızca kalanları listeler.
deja'nın temizlikten önce indekslediği oturumlar, dosya kaybolduktan sonra da aranabilir kalır. Ayrıntılar
[diskteki oturum dosyaları](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html) sayfasında.

</details>

<details>
<summary><b>Her şeyi nasıl silerim?</b></summary>

```sh
deja uninstall --all
rm -rf ~/.cache/deja
```

</details>

## Kılavuzlar

Özelliklere göre değil, durumlara göre yazıldı:

- [Bir kodlama ajanı önceki konuşmaları hatırlar mı?](https://vshulcz.github.io/deja-vu/guide/does-my-agent-remember.html) — her ajan oturumlar arasında neyi bırakıyor, neyi kaybediyor
- [Diskteki oturum dosyaları](https://vshulcz.github.io/deja-vu/guide/session-files-on-disk.html) — `~/.claude/projects` ne kadar büyür ve silmenin bedeli nedir
- [Ajan bağlamınızı az önce kaybetti](https://vshulcz.github.io/deja-vu/guide/lost-context.html) — çökmeden sonra, clear'dan sonra ya da oturum boş döndüğünde
- [Bağlam penceresi doldu](https://vshulcz.github.io/deja-vu/guide/context-window-full.html) — sıkıştırma gerçekte neyi koruyor, ölçümlerle, ve yerine ne yapılabilir
- [Dünkü oturuma devam etmek](https://vshulcz.github.io/deja-vu/guide/resume-a-session.html) — tüm ajanlar arasında bulup ait olduğu araçta açmak
- [Ajan, zaten düzelttiğiniz bir hatayı yeniden yaptı](https://vshulcz.github.io/deja-vu/guide/repeated-mistakes.html)
- [Bunun çözüldüğü oturumu bulmak](https://vshulcz.github.io/deja-vu/guide/find-a-session.html)
- [Ajanlar oturumlar arasında neden unutur](https://vshulcz.github.io/deja-vu/guide/forgetting.html) · [her ajan geçmişini nerede tutar](https://vshulcz.github.io/deja-vu/guide/where-sessions-are-stored.html)
- [Sıkıştırmanın kaybettikleri](https://vshulcz.github.io/deja-vu/guide/after-compaction.html) · [ajan değiştirmek](https://vshulcz.github.io/deja-vu/guide/switching-agents.html) · [ajanların ne yaptığını denetlemek](https://vshulcz.github.io/deja-vu/guide/auditing-agents.html) · [bir konuşmayı dışa aktarmak](https://vshulcz.github.io/deja-vu/guide/export-conversations.html) · [makineler arasında](https://vshulcz.github.io/deja-vu/guide/sync-across-machines.html) · [belleğin token maliyeti](https://vshulcz.github.io/deja-vu/guide/token-cost.html)

Araca göre: [opencode](https://vshulcz.github.io/deja-vu/guide/memory-for-opencode.html) · [Zed](https://vshulcz.github.io/deja-vu/guide/memory-for-zed.html) · [Grok Build](https://vshulcz.github.io/deja-vu/guide/memory-for-grok.html) · [Gemini CLI](https://vshulcz.github.io/deja-vu/guide/memory-for-gemini.html) · [OpenClaw](https://vshulcz.github.io/deja-vu/guide/memory-for-openclaw.html) · [Goose](https://vshulcz.github.io/deja-vu/guide/memory-for-goose.html) · [Cline](https://vshulcz.github.io/deja-vu/guide/memory-for-cline.html) · [pi ve omp](https://vshulcz.github.io/deja-vu/guide/memory-for-pi.html) · [Hermes](https://vshulcz.github.io/deja-vu/guide/memory-for-hermes.html)

## Kendi geçmişinizde deneyin

```sh
curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh
deja install --auto
```

Kurulum on saniye, indeksleme on saniye kadar. Bir ajan bir sonraki oturumu açtığında, bu projede neleri
çözdüğünüzü zaten biliyor olacak — deja'yı kurmanızdan öncekiler dahil.

## Geliştirme

`make build test lint`, ardından [CONTRIBUTING.md](../../CONTRIBUTING.md).
Yeni bir araç [ayrıştırıcı kaydından](../../docs/ARCHITECTURE.md#source-parsers) başlar.
Öncelikler ve yapmayacaklarımız [ROADMAP.md](../../ROADMAP.md) dosyasında.

## Lisans

MIT © [Vladislav Shulcz](https://github.com/vshulcz)
