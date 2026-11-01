(function(){
  var ORDER=[
    ['getting-started.html','Getting started'],
    ['agents.html','Agents & MCP'],
    ['search.html','Search'],
    ['commands.html','CLI reference'],
    ['harnesses.html','Harnesses'],
    ['privacy.html','Privacy'],
    ['benchmarks.html','Benchmarks'],
    ['compare.html','Compare']
  ];
  var art=document.querySelector('.doc article');
  if(!art)return;

  /* copy buttons on code blocks */
  art.querySelectorAll('pre').forEach(function(pre){
    var b=document.createElement('button');
    b.className='pre-copy';b.type='button';b.textContent='copy';
    b.addEventListener('click',function(){
      navigator.clipboard.writeText(pre.textContent.trim());
      b.textContent='copied';setTimeout(function(){b.textContent='copy'},1200);
    });
    /* a lid rather than a button floating over the code: a control that only
       appears on hover is a control most readers never learn about */
    var text=pre.textContent.trim();
    var what=/^[$#]|\bdeja\b|^curl|^brew|^go /.test(text)?'shell':
             /^[{[]/.test(text)?'json':'text';
    var lid=document.createElement('div');lid.className='pre-lid';
    lid.innerHTML='<span class="what">'+what+'</span>';
    lid.appendChild(b);
    var wrapEl=document.createElement('div');wrapEl.className='pre-wrap';
    pre.parentNode.insertBefore(wrapEl,pre);
    wrapEl.appendChild(lid);wrapEl.appendChild(pre);
  });

  /* anchors on h2 */
  art.querySelectorAll('h2').forEach(function(h){
    if(!h.id)h.id=h.textContent.trim().toLowerCase().replace(/[^a-z0-9]+/g,'-').replace(/^-|-$/g,'');
    var a=document.createElement('a');
    a.className='hlink';a.href='#'+h.id;a.textContent=' #';a.setAttribute('aria-label','link to section');
    h.appendChild(a);
  });

  /* prev / next */
  var here=location.pathname.split('/').pop()||ORDER[0][0];
  var i=ORDER.findIndex(function(x){return x[0]===here});
  if(i>=0){
    var nav=document.createElement('div');nav.className='pn';
    var prev=i>0?ORDER[i-1]:null,next=i<ORDER.length-1?ORDER[i+1]:null;
    /* the name alone does not say which way it goes, and an arrow alone does
       not say where. Both, with the direction as the quiet half. */
    nav.innerHTML=(prev?'<a class="pv" href="'+prev[0]+'"><small>Previous</small>'+prev[1]+'</a>':'<span></span>')+
                  (next?'<a class="nx" href="'+next[0]+'"><small>Next</small>'+next[1]+'</a>':'<span></span>');
    art.appendChild(nav);
  }

  /* ── how long the page takes ───────────────────────────────────────── */
  (function(){
    var slot=art.querySelector('[data-mins]');
    if(!slot)return;
    var words=art.textContent.trim().split(/\s+/).length;
    slot.textContent=Math.max(1,Math.round(words/220))+' min read';
  })();

  /* ── the sidebar remembers what you have read ──────────────────────────
     The same idea the home page greets you with. Of every documentation site
     that could do this, one about a memory tool has the most right to. */
  (function(){
    var KEY='deja.read';
    var here=location.pathname.split('/').pop()||ORDER[0][0];
    var read={};
    try{read=JSON.parse(localStorage.getItem(KEY)||'{}')}catch(e){}
    read[here]=1;
    try{localStorage.setItem(KEY,JSON.stringify(read))}catch(e){}

    var side=document.querySelector('.doc aside');
    if(!side)return;
    var n=0;
    ORDER.forEach(function(pair){
      var a=side.querySelector('a[href="'+pair[0]+'"]');
      if(!a)return;
      if(read[pair[0]]){a.classList.add('read');a.title='read';n++}
    });
    var grp=side.querySelector('.grp');
    if(grp&&n){
      var c=document.createElement('span');
      c.className='count';c.textContent=n+'/'+ORDER.length;
      grp.appendChild(c);
    }
  })();

  /* ── say that the guide can be searched ────────────────────────────── */
  var docLang=(document.documentElement.lang||'en').toLowerCase();
  var isRu=docLang.indexOf('ru')===0,isZh=docLang.indexOf('zh')===0;
  (function(){
    var nav=document.querySelector('nav .spacer');
    if(!nav)return;
    var hint=document.createElement('button');
    hint.className='searchhint';hint.type='button';
    hint.innerHTML='<span>'+(isRu?'поиск по документации':isZh?'搜索指南':'search the guide')+'</span><kbd>/</kbd>';
    hint.addEventListener('click',function(){
      document.dispatchEvent(new KeyboardEvent('keydown',{key:'/',bubbles:true}));
    });
    nav.parentNode.insertBefore(hint,nav.nextSibling);

    /* upgrade the top nav with a Demo link and a prominent ★ GitHub button */
    var gh=nav.parentNode.querySelector('a[href*="github.com/vshulcz/deja-vu"]');
    if(gh){
      gh.className='nav-cta';
      gh.innerHTML='<span class="star-ic" aria-hidden="true">★</span>GitHub';
      var homeLink=nav.parentNode.querySelector('a.brand');
      var rootPrefix=homeLink?homeLink.getAttribute('href'):'../';
      if(rootPrefix.slice(-1)!=='/')rootPrefix+='/';
      if(!nav.parentNode.querySelector('a[href*="view/"]')){
        var demo=document.createElement('a');
        demo.className='lnk';
        demo.href=rootPrefix+'view/';
        demo.textContent=isRu?'Демо':isZh?'演示':'Demo';
        nav.parentNode.insertBefore(demo,gh);
      }
    }
  })();

  /* ── enrich the closing star CTA with a 1-click install & live demo ─── */
  (function(){
    var cta=art.querySelector('.star-cta');
    if(!cta){
      cta=document.createElement('p');
      cta.className='star-cta';
      art.appendChild(cta);
    }
    var homeLink=document.querySelector('nav a.brand');
    var rootPrefix=homeLink?homeLink.getAttribute('href'):'../';
    if(rootPrefix.slice(-1)!=='/')rootPrefix+='/';
    var cmdText='curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh';
    var leadTxt=isRu?'Установка за 10 секунд, индекс за 10.':isZh?'十秒安装，十秒建索引。':'Ten seconds to install, ten to index.';
    var starTxt=isRu?'★ Поставить звезду на GitHub':isZh?'★ 在 GitHub 上点 Star':'★ Star deja-vu on GitHub';
    var demoTxt=isRu?'Живое демо в браузере →':isZh?'浏览器在线演示 →':'Try live browser demo →';
    cta.innerHTML=
      '<div class="cta-cmd"><span class="p">$</span><code>'+cmdText+'</code><button type="button">copy</button></div>'+
      '<div class="cta-row"><span>'+leadTxt+'</span>'+
      '<a class="cta-btn" href="https://github.com/vshulcz/deja-vu">'+starTxt+'</a>'+
      '<a class="cta-demo" href="'+rootPrefix+'view/">'+demoTxt+'</a></div>';
    var btn=cta.querySelector('button');
    if(btn){
      btn.addEventListener('click',function(){
        navigator.clipboard.writeText(cmdText);
        btn.textContent='copied';
        setTimeout(function(){btn.textContent='copy'},1200);
      });
    }

    var saveFirst=art.querySelector('.save-first');
    if(saveFirst&&!saveFirst.querySelector('.save-actions')){
      var saveCmd='curl -fsSL https://raw.githubusercontent.com/vshulcz/deja-vu/main/install.sh | sh && deja index';
      var bar=document.createElement('div');
      bar.className='save-actions';
      bar.innerHTML=
        '<div class="save-cmd"><span class="p">$</span><code>'+saveCmd+'</code><button type="button">copy</button></div>'+
        '<a class="save-btn" href="https://github.com/vshulcz/deja-vu">'+starTxt+'</a>'+
        '<a class="save-demo" href="'+rootPrefix+'view/">'+demoTxt+'</a>';
      saveFirst.appendChild(bar);
      var sbtn=bar.querySelector('button');
      if(sbtn){
        sbtn.addEventListener('click',function(){
          navigator.clipboard.writeText(saveCmd);
          sbtn.textContent='copied';
          setTimeout(function(){sbtn.textContent='copy'},1200);
        });
      }
    }
  })();


  /* ── the sidebar folds on a phone ───────────────────────────────────────
     Eleven links and a cat above the article means scrolling past the whole
     guide to reach the page you opened. */
  (function(){
    var side=document.querySelector('.doc aside');
    if(!side||!matchMedia('(max-width:860px)').matches)return;

    var here=side.querySelector('a[aria-current]');
    var box=document.createElement('details');
    box.className='sidefold';
    var head=document.createElement('summary');
    /* "Codex" alone could be the delete page or the memory one */
    var grp=here&&here.closest('details.grpfold');
    var where=grp?grp.querySelector('summary').firstChild.textContent.trim()+' · ':'';
    head.innerHTML='<span>Guide</span><b>'+(here?where+here.textContent.trim():'contents')+'</b>';
    box.appendChild(head);
    while(side.firstChild)box.appendChild(side.firstChild);
    side.appendChild(box);

    /* opening it should not leave the reader somewhere else when it closes */
    box.addEventListener('toggle',function(){
      if(!box.open)side.scrollIntoView({block:'nearest'});
    });
  })();

  /* new page behaviour goes above this line, inside this closure */

  /* ── a hairline that fills as you read ─────────────────────────────── */
  (function(){
    if(matchMedia('(prefers-reduced-motion: reduce)').matches)return;
    var bar=document.createElement('div');bar.className='progress';
    document.body.appendChild(bar);
    addEventListener('scroll',function(){
      var h=document.documentElement.scrollHeight-innerHeight;
      bar.style.transform='scaleX('+(h>0?scrollY/h:0)+')';
    },{passive:true});
  })();

  /* ── contents, with the section you are in marked ──────────────────── */
  (function(){
    var heads=[].slice.call(art.querySelectorAll('h2'));
    if(heads.length<3)return;               /* three sections is where a list starts earning its place */
    /* a div, not a nav: the stylesheet's nav rule is the page header — flex,
       sticky, padded — and it applied to this one too, laying the sections out
       in a row that ran off the side of the page */
    var toc=document.createElement('div');
    toc.className='toc';toc.setAttribute('role','navigation');
    toc.setAttribute('aria-label','on this page');
    toc.innerHTML='<div class="grp">On this page</div>'+heads.map(function(h){
      return '<a href="#'+h.id+'">'+h.firstChild.textContent.trim()+'</a>';
    }).join('');
    document.querySelector('.doc').appendChild(toc);

    var links=[].slice.call(toc.querySelectorAll('a'));
    var seen=new Map();
    var io=new IntersectionObserver(function(es){
      es.forEach(function(e){seen.set(e.target,e.isIntersecting)});
      /* the first visible heading wins, so scrolling up marks the one you are
         arriving at rather than the one you are leaving */
      var current=heads.find(function(h){return seen.get(h)})||null;
      links.forEach(function(a,i){
        a.classList.toggle('on',current?heads[i]===current:i===0);
      });
    },{rootMargin:'-72px 0px -70% 0px'});
    heads.forEach(function(h){io.observe(h)});
  })();

  /* ── the docs are searchable ───────────────────────────────────────────
     A tool for searching your own history whose own pages cannot be searched
     is arguing against itself. The index is built in the browser, on the first
     keystroke, from the pages themselves — no service, nothing sent. */
  (function(){
    var box=document.createElement('div');
    box.className='palette';box.id='palette';box.setAttribute('aria-hidden','true');
    box.innerHTML='<div class="pbox"><input id="pq" type="search" autocomplete="off" '+
      'spellcheck="false" placeholder="search the guide…" aria-label="search the guide">'+
      '<div class="phits" id="phits"></div>'+
      '<div class="pfoot"><span><kbd>↑↓</kbd> move · <kbd>↵</kbd> open · <kbd>esc</kbd> close</span>'+
      '<span class="plocal">indexed in your browser</span></div></div>';
    document.body.appendChild(box);

    var input=box.querySelector('#pq'),hits=box.querySelector('#phits');
    var docs=null,loading=null,sel=0,rows=[];

    function load(){
      if(docs)return Promise.resolve(docs);
      if(loading)return loading;
      /* Index every sidebar link immediately so all 95 guide pages and 43
         coding agents are reachable on the first keystroke, not only the 8
         pages in ORDER. */
      var seenUrls={};
      ORDER.forEach(function(pair){seenUrls[pair[0]]=true});
      var sidePages=[];
      document.querySelectorAll('.doc aside a[href$=".html"]').forEach(function(a){
        var url=a.getAttribute('href');
        if(!url||url.indexOf('..')===0||url.indexOf('http')===0||seenUrls[url])return;
        seenUrls[url]=true;
        var grp=a.closest('details.grpfold');
        var cat=grp?grp.querySelector('summary').firstChild.textContent.trim():'Guide';
        var label=a.textContent.trim();
        sidePages.push({page:cat,url:url,title:label,body:cat+' '+label+' '+url.replace(/[-.]/g,' ')});
      });
      loading=Promise.all(ORDER.map(function(pair){
        return fetch(pair[0]).then(function(r){return r.text()}).then(function(html){
          var d=new DOMParser().parseFromString(html,'text/html');
          var a=d.querySelector('.doc article');
          if(!a)return [];
          return [].slice.call(a.querySelectorAll('h1,h2')).map(function(h){
            var body='',n=h.nextElementSibling;
            while(n&&!/^H[12]$/.test(n.tagName)){body+=' '+n.textContent;n=n.nextElementSibling}
            var id=h.id||h.textContent.trim().toLowerCase().replace(/[^a-z0-9]+/g,'-').replace(/^-|-$/g,'');
            return {page:pair[1],url:pair[0]+(h.tagName==='H1'?'':'#'+id),
                    title:h.textContent.replace(/\s*#$/,'').trim(),body:body.slice(0,600)};
          });
        }).catch(function(){return []});
      })).then(function(all){docs=[].concat.apply([],all).concat(sidePages);return docs});
      return loading;
    }

    function run(q){
      q=q.trim().toLowerCase();
      if(!q){hits.innerHTML='<p class="pnone">every page of the guide, searchable from here</p>';rows=[];return}
      load().then(function(all){
        var words=q.split(/\s+/);
        var scored=all.map(function(d){
          var t=d.title.toLowerCase(),b=d.body.toLowerCase(),score=0;
          words.forEach(function(w){
            if(t.indexOf(w)>=0)score+=8;
            if(b.indexOf(w)>=0)score+=1;
          });
          return {d:d,score:score};
        }).filter(function(x){return x.score>0})
          .sort(function(a,b){return b.score-a.score}).slice(0,7);
        rows=scored.map(function(x){return x.d});
        sel=0;
        hits.innerHTML=rows.length?rows.map(function(d,i){
          return '<a class="phit'+(i?'':' on')+'" href="'+d.url+'"><b>'+d.title+
                 '</b><span>'+d.page+'</span></a>';
        }).join(''):'<p class="pnone">nothing here — try recall, mcp, redaction</p>';
      });
    }

    function open(){box.classList.add('on');box.setAttribute('aria-hidden','false');
      input.value='';run('');input.focus()}
    function close(){box.classList.remove('on');box.setAttribute('aria-hidden','true')}

    input.addEventListener('input',function(){run(input.value)});
    box.addEventListener('click',function(e){if(e.target===box)close()});
    addEventListener('keydown',function(e){
      var tag=(e.target.tagName||'').toLowerCase();
      if((e.key==='k'&&(e.metaKey||e.ctrlKey))||(e.key==='/'&&tag!=='input'&&tag!=='textarea')){
        e.preventDefault();box.classList.contains('on')?close():open();return;
      }
      if(!box.classList.contains('on'))return;
      if(e.key==='Escape')return close();
      if(e.key==='ArrowDown'||e.key==='ArrowUp'){
        e.preventDefault();
        var list=hits.querySelectorAll('.phit');
        if(!list.length)return;
        sel=(sel+(e.key==='ArrowDown'?1:list.length-1))%list.length;
        list.forEach(function(el,i){el.classList.toggle('on',i===sel)});
      }
      if(e.key==='Enter'&&rows[sel])location.href=rows[sel].url;
    });
  })();

  /* ── the cat knows where it is ─────────────────────────────────────── */
  (function(){
    var cat=document.querySelector('.sidecat');
    if(!cat)return;
    var says=['this page is indexed too','press / to search it','i was here in March',
              'nothing left this machine','purr'];
    var i=0,label=cat.querySelector('span'),was=label?label.textContent:'';
    cat.addEventListener('click',function(e){
      e.preventDefault();
      if(!label)return;
      label.textContent=says[i++%says.length];
      clearTimeout(cat._t);
      cat._t=setTimeout(function(){label.textContent=was},2400);
    });
  })();
})();

/* Record the page for the home page's greeting. Same store, same rule as the
   tool itself: it stays in this browser. */
(function(){
  var title=(document.querySelector('h1')||{}).textContent||'';
  title=title.replace(/^[$#\s]+/,'').trim();
  if(!title)return;
  try{
    var KEY='deja.visits';
    var s=JSON.parse(localStorage.getItem(KEY)||'null')||{n:0};
    s.page=title;
    localStorage.setItem(KEY,JSON.stringify(s));
  }catch(e){}


})();
