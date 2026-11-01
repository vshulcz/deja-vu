package index

import (
	"strings"

	"github.com/vshulcz/deja-vu/internal/cjkfold"
	"github.com/vshulcz/deja-vu/internal/query"
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
	strong []int, idfOf map[string]float64) ([]SessionMeta, []int, []int, map[string]float64, bool) {
	was := 0
	for _, v := range strong {
		if v > was {
			was = v
		}
	}
	neighbours := readCooccur(dir)
	inProject := map[uint32]bool{}
	scope := newProjectScope(projects...)
	for _, meta := range m.Sessions {
		if scope.has(meta.Project) {
			inProject[meta.Ord] = true
		}
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
		if idfOf[t] < dejaVuStrongIDFFloor {
			continue
		}
		tPosts, terr := postingsFor(dir, "t"+t)
		if terr != nil || len(tPosts) == 0 {
			continue
		}
		at := make(map[int64]bool, len(tPosts))
		for _, pp := range tPosts {
			if inProject[pp.Sid] {
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
				if inProject[pp.Sid] && at[pp.Off] {
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
		for _, cand := range crossScriptBridgeTerms(terms) {
			if have[cand] {
				continue
			}
			cPosts, cerr := postingsFor(dir, "t"+cjkfold.String(cand))
			if cerr != nil || len(cPosts) == 0 {
				continue
			}
			inProj := false
			for _, pp := range cPosts {
				if inProject[pp.Sid] {
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
		return nil, nil, nil, nil, false
	}
	metas, matched, bstrong, idf, err := relevantMetasMatched(dir, m, projects,
		append(append([]string{}, terms...), added...), n, skip)
	if err != nil || len(metas) == 0 {
		return nil, nil, nil, nil, false
	}
	// Taken only when the bridge found something more identifying than the
	// question's own words did. Otherwise the project has no better answer and
	// the first ranking stands.
	for i, v := range bstrong {
		if v > was || (crossScript && i < len(matched) && matched[i] >= 2) {
			return metas, matched, bstrong, idf, true
		}
	}
	return nil, nil, nil, nil, false
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
				if t == ru || (len(ru) >= 4 && strings.HasPrefix(t, ru)) {
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
