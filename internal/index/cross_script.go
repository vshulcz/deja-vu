package index

import (
	"math"
	"strings"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/search"
)

const bridgeTerms = 4

// bridgedRetry answers a question asked in words the project does not use.
//
// The ranking has just read every term and nothing it found identifies
// anything: no session holds a word of this question rare enough to be worth an
// unprompted recall. That is what a rephrasing looks like from inside — the
// answer is there in the words the project actually uses, and the question
// shares none of them.
//
// The corpus knows the pairing: a session that says "the debounce is how long
// we wait before sending" puts both vocabularies in one message, and the
// co-occurrence map records it. But that map is built over the whole store,
// and a question about something this project never held is made of ordinary
// words whose neighbours are every subject any project ever had — so the map
// alone answers both kinds of question with equal confidence. Measured on the
// bench's negative controls, bridging on the map alone fired on three of
// twelve questions with no answer to find.
//
// So the tie has to be the project's own. A candidate is kept only when some
// record in this project says it in the same breath as the question's word,
// which is the sentence that explains the term — the thing a rephrasing has
// and an absent subject does not.
func bridgedRetry(dir string, m Manifest, projects, terms []string, n int, skip map[string]bool,
	strong []int, idfOf map[string]float64) ([]SessionMeta, []int, []int, []int, map[string]float64, map[uint32][]int64, bool) {
	was := 0
	for _, v := range strong {
		if v > was {
			was = v
		}
	}
	hasStrongTerm := hasTermAboveFloor(terms, idfOf, dejaVuStrongIDFFloor)
	var crossCands []string
	if was == 0 {
		crossCands = crossScriptBridgeTerms(terms)
	}
	if !hasStrongTerm && len(crossCands) == 0 {
		return nil, nil, nil, nil, nil, nil, false
	}
	var inProject map[uint32]bool
	projectOrds := func() map[uint32]bool {
		if inProject == nil {
			inProject = projectOrdSet(m, projects)
		}
		return inProject
	}
	var neighbours map[string][]string
	if hasStrongTerm && !shouldBlockSameScriptBridge(dir, m, terms, idfOf, projectOrds) {
		neighbours = readCooccur(dir)
	}
	have := make(map[string]bool, len(terms))
	for _, t := range terms {
		have[t] = true
	}
	added := make([]string, 0, bridgeTerms)
	for _, t := range terms {
		if neighbours == nil {
			break
		}
		// Bridge from a word that means something. "open the file and read it"
		// is three ordinary words, and the corpus's neighbours for ordinary
		// words are its subjects — that sentence was the one negative control
		// this fired on.
		if idfOf[t] < dejaVuStrongIDFFloor || len(neighbours[t]) == 0 {
			continue
		}
		tPosts, terr := postingsFor(dir, "t"+t)
		if terr != nil || len(tPosts) == 0 {
			continue
		}
		inProj := projectOrds()
		at := make(map[int64]bool, len(tPosts))
		for _, pp := range tPosts {
			if inProj[pp.Sid] {
				at[pp.Off] = true
			}
		}
		if len(at) == 0 {
			continue
		}
		for _, cand := range neighbours[t] {
			if have[cand] || query.IsStopWord(cand) {
				continue
			}
			cPosts, cerr := postingsFor(dir, "t"+cand)
			if cerr != nil || len(cPosts) == 0 {
				continue
			}
			tied := false
			for _, pp := range cPosts {
				if inProj[pp.Sid] && at[pp.Off] {
					tied = true
					break
				}
			}
			if !tied {
				continue
			}
			have[cand] = true
			added = append(added, cand)
			if len(added) == bridgeTerms {
				break
			}
		}
		if len(added) == bridgeTerms {
			break
		}
	}
	crossScript := false
	if was == 0 && len(added) < bridgeTerms {
		for _, cand := range crossCands {
			if have[cand] {
				continue
			}
			cPosts, cerr := postingsFor(dir, "t"+cjkfold.String(cand))
			if cerr != nil || len(cPosts) == 0 {
				continue
			}
			inProjMap := projectOrds()
			inProj := false
			for _, pp := range cPosts {
				if inProjMap[pp.Sid] {
					inProj = true
					break
				}
			}
			if !inProj {
				continue
			}
			have[cand] = true
			added = append(added, cand)
			crossScript = true
			if len(added) == bridgeTerms {
				break
			}
		}
	}
	if len(added) == 0 {
		return nil, nil, nil, nil, nil, nil, false
	}
	var keep func(SessionMeta) bool
	if len(skip) > 0 {
		keep = func(meta SessionMeta) bool { return !skip[meta.ID] }
	}
	rank, err := relevantMetasCounts(dir, m, projects,
		append(append([]string{}, terms...), added...), n, keep)
	if err != nil || len(rank.metas) == 0 {
		return nil, nil, nil, nil, nil, nil, false
	}
	// Taken only when the bridge found something more identifying than the
	// question's own words did. Otherwise the project has no better answer and
	// the first ranking stands.
	for i, v := range rank.strong {
		if v > was || (crossScript && i < len(rank.informative) && rank.informative[i] >= 2) {
			return rank.metas, rank.informative, rank.strong, rank.naming, rank.idf, rank.offsets, true
		}
	}
	return nil, nil, nil, nil, nil, nil, false
}

// shouldBlockSameScriptBridge reports whether a strong ASCII identifier in the query
// exists elsewhere in the corpus yet has zero postings in the target projects,
// unless at least two in-project strong terms already anchor the question.
// Blocking same-script neighbour expansion in that case prevents an unrelated
// high-IDF verb from pulling in a co-occurring neighbour and fabricating a
// multi-term match when the actual subject is absent.
func shouldBlockSameScriptBridge(dir string, m Manifest, terms []string, idfOf map[string]float64, projectOrds func() map[uint32]bool) bool {
	blockFloor := math.Min(search.IdentifierIDFFloor, topIDF(len(m.Sessions)))
	seenStrong := 0
	seenIdent := 0
	for _, t := range terms {
		if idfOf[t] >= dejaVuStrongIDFFloor {
			seenStrong++
		}
		if idfOf[t] >= blockFloor && search.HasIdentifierTerm([]string{t}) {
			seenIdent++
		}
	}
	if seenStrong >= 2 || (len(terms) > 3 && seenIdent >= 1 && seenStrong >= 1) {
		return false
	}
	for _, t := range terms {
		if !hasASCIILetterOrDigit(t) || len(t) < 3 || idfOf[t] > 0 || !search.HasIdentifierTerm([]string{t}) {
			continue
		}
		tPosts, err := postingsFor(dir, "t"+t)
		if err != nil || len(tPosts) == 0 {
			continue
		}
		inProj := projectOrds()
		seenInProj := false
		seenSids := make(map[uint32]bool, len(tPosts))
		for _, pp := range tPosts {
			seenSids[pp.Sid] = true
			if inProj[pp.Sid] {
				seenInProj = true
				break
			}
		}
		if seenInProj {
			continue
		}
		if rankIDF(len(m.Sessions), len(seenSids)) >= blockFloor {
			return true
		}
	}
	return false
}

func hasTermAboveFloor(terms []string, idfOf map[string]float64, floor float64) bool {
	for _, t := range terms {
		if idfOf[t] >= floor {
			return true
		}
	}
	return false
}

func projectOrdSet(m Manifest, projects []string) map[uint32]bool {
	inProject := map[uint32]bool{}
	scope := newProjectScope(projects...)
	all := viewsFor(m).allMeta()
	for i := range all {
		if scope.has(all[i].Project) {
			inProject[all[i].Ord] = true
		}
	}
	return inProject
}

type trilingualConcept struct {
	en []string
	ru []string
	zh []string
}

var trilingualLexicon = []trilingualConcept{
	{en: []string{"timeout"}, ru: []string{"таймаут"}, zh: []string{"超时"}},
	{en: []string{"deadlock", "lock"}, ru: []string{"дедлок", "блокировк"}, zh: []string{"死锁", "加锁"}},
	{en: []string{"memory", "leak"}, ru: []string{"утечк", "памят"}, zh: []string{"内存", "泄漏"}},
	{en: []string{"database", "sqlite"}, ru: []string{"база", "базы", "базе"}, zh: []string{"数据", "据库"}},
	{en: []string{"pool", "connection"}, ru: []string{"пул", "пула", "пуле", "соединен"}, zh: []string{"连接", "接池"}},
	{en: []string{"build", "compile"}, ru: []string{"сборк", "компиляц"}, zh: []string{"构建", "编译"}},
	{en: []string{"cache"}, ru: []string{"кэш", "кеш"}, zh: []string{"缓存"}},
	{en: []string{"config"}, ru: []string{"конфиг", "настройк"}, zh: []string{"配置"}},
	{en: []string{"deploy"}, ru: []string{"депло", "выкат"}, zh: []string{"部署"}},
	{en: []string{"container", "docker"}, ru: []string{"контейнер", "докер"}, zh: []string{"容器", "镜像"}},
	{en: []string{"branch"}, ru: []string{"ветк"}, zh: []string{"分支"}},
	{en: []string{"merge"}, ru: []string{"мерж", "смерж", "влит"}, zh: []string{"合并"}},
	{en: []string{"hook"}, ru: []string{"хук"}, zh: []string{"钩子"}},
	{en: []string{"index"}, ru: []string{"индекс"}, zh: []string{"索引"}},
	{en: []string{"auth", "token"}, ru: []string{"авторизац", "токен"}, zh: []string{"认证", "鉴权"}},
	{en: []string{"permission"}, ru: []string{"права", "доступ"}, zh: []string{"权限"}},
	{en: []string{"port"}, ru: []string{"порт"}, zh: []string{"端口"}},
	{en: []string{"proxy"}, ru: []string{"прокси"}, zh: []string{"代理"}},
	{en: []string{"retry"}, ru: []string{"ретра", "повтор"}, zh: []string{"重试"}},
	{en: []string{"panic", "crash", "fail"}, ru: []string{"паник", "краш", "падает", "упал"}, zh: []string{"崩溃", "失败"}},
	{en: []string{"latency"}, ru: []string{"задержк", "латентн"}, zh: []string{"延迟"}},
	{en: []string{"queue"}, ru: []string{"очеред"}, zh: []string{"队列"}},
	{en: []string{"worker", "goroutine"}, ru: []string{"воркер", "горутин"}, zh: []string{"协程"}},
	{en: []string{"migration"}, ru: []string{"миграц"}, zh: []string{"迁移"}},
	{en: []string{"localize", "localization"}, ru: []string{"локализ", "таргет"}, zh: []string{"定位"}},
	{en: []string{"inject", "injection"}, ru: []string{"инъекц", "вставк"}, zh: []string{"注入"}},
	{en: []string{"dedup", "dedupe", "duplicate"}, ru: []string{"дедуп", "дубликат"}, zh: []string{"去重"}},
	{en: []string{"compact", "compaction"}, ru: []string{"компакт", "сжат"}, zh: []string{"压缩"}},
	{en: []string{"prompt"}, ru: []string{"промпт", "запрос"}, zh: []string{"提示词"}},
	{en: []string{"bench", "benchmark"}, ru: []string{"бенч", "бенчмарк", "замер"}, zh: []string{"基准"}},
	{en: []string{"session"}, ru: []string{"сесси", "истори"}, zh: []string{"会话"}},
	{en: []string{"search", "recall"}, ru: []string{"поиск", "воспоминан"}, zh: []string{"检索", "召回"}},
	{en: []string{"snippet", "quote"}, ru: []string{"сниппет", "цитат"}, zh: []string{"片段", "引用"}},
	{en: []string{"subagent"}, ru: []string{"сабагент", "субагент"}, zh: []string{"子代理"}},
	{en: []string{"context"}, ru: []string{"контекст"}, zh: []string{"上下文"}},
	{en: []string{"policy", "rule"}, ru: []string{"правил", "политик"}, zh: []string{"规则", "策略"}},
	{en: []string{"filter", "noise"}, ru: []string{"фильтр", "шум"}, zh: []string{"过滤", "噪声"}},
	{en: []string{"paraphrase"}, ru: []string{"парафраз", "перефраз"}, zh: []string{"改写"}},
	{en: []string{"symbol", "signature"}, ru: []string{"символ", "сигнатур"}, zh: []string{"符号", "签名"}},
	{en: []string{"install", "setup"}, ru: []string{"установк", "инсталл"}, zh: []string{"安装"}},
	{en: []string{"doctor", "diagnostic"}, ru: []string{"доктор", "диагностик"}, zh: []string{"诊断"}},
	{en: []string{"delete", "remove"}, ru: []string{"удален", "удалит"}, zh: []string{"删除"}},
	{en: []string{"ingest", "import"}, ru: []string{"инжест", "импорт"}, zh: []string{"导入"}},
	{en: []string{"manifest"}, ru: []string{"манифест"}, zh: []string{"清单"}},
	{en: []string{"table", "schema"}, ru: []string{"таблиц", "схем"}, zh: []string{"表结构"}},
	{en: []string{"redact", "secret"}, ru: []string{"секрет", "маскир", "редакц"}, zh: []string{"脱敏"}},
	{en: []string{"stream", "jsonl"}, ru: []string{"стрим"}, zh: []string{"流式"}},
	{en: []string{"spawn"}, ru: []string{"спавн"}, zh: []string{"派生"}},
	{en: []string{"ranking", "score"}, ru: []string{"ранжир", "скоринг"}, zh: []string{"排序"}},
}

// CrossScriptTerms maps domain terms in one script to known equivalents in other scripts.
func CrossScriptTerms(terms []string) []string {
	return crossScriptBridgeTerms(terms)
}

// crossScriptBridgeTerms maps technical terms in one script (Latin, Cyrillic,
// CJK) to their equivalents in the OTHER scripts only, so a Russian or Chinese
// question about an English codebase (or an English recall against a Chinese or
// Russian session) bridges across scripts without perturbing same-script queries.
func crossScriptBridgeTerms(terms []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(list []string) {
		for _, w := range list {
			if !seen[w] {
				seen[w] = true
				out = append(out, w)
			}
		}
	}
	for _, raw := range terms {
		t := cjkfold.String(query.Lower(raw))
		for _, c := range trilingualLexicon {
			for _, zh := range c.zh {
				if t == zh {
					add(c.en)
					add(c.ru)
				}
			}
			for _, ru := range c.ru {
				if t == ru || (len(ru) >= 4 && strings.HasPrefix(t, ru) && (ru != "правил" || !strings.HasPrefix(t, "правиль"))) {
					add(c.en)
					add(c.zh)
				}
			}
			for _, en := range c.en {
				if t == en {
					add(c.ru)
					add(c.zh)
				}
			}
		}
	}
	return out
}
