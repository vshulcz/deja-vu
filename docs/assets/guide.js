(function(){
  var ORDER=[
    ['getting-started.html','Getting started'],
    ['agents.html','Agents & MCP'],
    ['search.html','Search'],
    ['commands.html','CLI reference'],
    ['harnesses.html','Harnesses'],
    ['privacy.html','Privacy'],
    ['benchmarks.html','Benchmarks']
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
    var wrapEl=document.createElement('div');wrapEl.className='pre-wrap';
    pre.parentNode.insertBefore(wrapEl,pre);wrapEl.appendChild(pre);wrapEl.appendChild(b);
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
    nav.innerHTML=(prev?'<a class="pv" href="'+prev[0]+'">&larr; '+prev[1]+'</a>':'<span></span>')+
                  (next?'<a class="nx" href="'+next[0]+'">'+next[1]+' &rarr;</a>':'<span></span>');
    art.appendChild(nav);
  }
})();
