(function () {
  const pages = {overview:'运行概览',context:'当前上下文',messages:'消息记录',activity:'活动日志',agent:'Agent 管理',qq:'QQ 管理',config:'配置'};
  const kinds = {prompt:'提示词',input:'消息输入',state:'状态',summary:'工作摘要',output:'模型输出',tool_result:'工具回执',tool_definition:'工具定义',turn_start:'活动开始',reasoning:'推理',tool_call:'工具调用',reply:'已发送',usage:'用量',finish:'活动结束',error:'错误',config:'配置'};
  const clone = v => JSON.parse(JSON.stringify(v));
  const emptyAgent = () => ({models:{},wake_routes:{},context:{},history:{},modules:{}});
  const emptyBusy = () => Object.fromEntries(['login','refresh','detail','messages','search','mood','note','wake','timer','prompt','members','napcat','config','modelTest'].map(k=>[k,false]));
  window.qmiConsole = function () {
    // Transport objects stay outside Alpine's reactive proxy.
    const transport = {generation:0, controllers:new Set(), timer:null, refreshing:false, chatGeneration:0, contextGeneration:0, promptGeneration:0, toastTimer:null, observer:null};
    return {
      page:'overview', theme:localStorage.getItem('qmi_theme') || 'dark', drawerOpen:false,
      authenticated:false, token:localStorage.getItem('qmi_token') || '', tokenInput:'',
      loginError:'', connection:'connected', busy:emptyBusy(), errors:{}, lastSuccess:{}, toast:'',
      runtime:{}, overview:{}, events:[], afterID:0, followEvents:true, newEventCount:0,
      eventFilters:{kind:'',source:'',q:''},
      contextQuery:{source:'working',kind:'',scope:'',q:'',offset:0}, contextPage:{entries:[]},
      contextDetail:null, contextConflict:'',
      chats:[], chatFilter:'', selectedChat:null, messages:[], hasOlder:false,
      messageSearch:'', searchScope:'all', searchResults:null, highlightMessageID:'',
      agentTab:'state', notes:[], moodDraft:'', moodBaseline:'', noteDraft:'', wakeDraft:'',
      timers:[], timerDelay:600, timerNote:'', tasks:[],
      promptName:'persona', promptActiveName:'', promptPath:'', promptDraft:'', promptBaseline:'', promptLoaded:false,
      qqTab:'groups', groups:[], friends:[], qqFilter:'', memberGroup:null, memberData:{},
      napActions:[], napDangerous:[], napAction:'', napParams:'{}', napConfirmed:false, napResult:'',
      configTab:'models', configLoaded:false, configView:{}, configReview:null,
      configDraft:{models:emptyAgent(),behavior:emptyAgent(),modules:emptyAgent()},
      configBaseline:{models:emptyAgent(),behavior:emptyAgent(),modules:emptyAgent()},
      newTier:'', moduleNames:['core','memory','sticker','qzone','admin','computer','browser','system'],
      get pageTitle() { return pages[this.page] || pages.overview; },
      get promptDirty() { return this.promptLoaded && this.promptDraft !== this.promptBaseline; },
      get filteredChats() { const q=this.chatFilter.toLowerCase();return this.chats.filter(c=>(this.chatName(c)+' '+c.last_text).toLowerCase().includes(q)); },
      get filteredEvents() {
        const f=this.eventFilters;
        return this.events.filter(e=>(!f.kind||e.kind===f.kind)&&(!f.source||(e.chat_type+':'+e.chat_id).includes(f.source))&&(!f.q||e.text.toLowerCase().includes(f.q.toLowerCase())));
      },
      get readonlyConfig() {
        const e=this.configView.effective||{},a=e.agent||{};
        return {agent:{enable:a.enable,browser_executable:a.browser_executable,workspace_dir:a.workspace_dir,image_dir:a.image_dir},bot:{qq_number:e.bot?.qq_number},webui:{host:e.webui?.host,port:e.webui?.port},storage:e.storage};
      },
      init() {
        this.page=pages[location.hash.slice(1)]?location.hash.slice(1):'overview';
        document.documentElement.dataset.theme=this.theme;
        this.$watch('theme',v=>{localStorage.setItem('qmi_theme',v);document.documentElement.dataset.theme=v;});
        window.addEventListener('hashchange',()=> {
          const next=location.hash.slice(1);
          if (pages[next] && next!==this.page) this.navigate(next,true);
        });
        document.addEventListener('visibilitychange',()=> {
          this.stopPageRefresh(); if (!document.hidden && this.authenticated) this.refreshPage();
        });
        window.addEventListener('beforeunload',e=>{if(this.anyDirty()){e.preventDefault();e.returnValue='';}});
        document.addEventListener('keydown',e=>this.trapFocus(e));
        this.$nextTick(()=> {
          this.icons();
          transport.observer=new MutationObserver(records=>{
            if(records.some(r=>Array.from(r.addedNodes).some(n=>n.nodeType===1&&(n.matches?.('i[data-lucide]')||n.querySelector?.('i[data-lucide]'))))) this.icons();
          });
          transport.observer.observe(document.body,{childList:true,subtree:true});
        });
        if(this.token) { this.tokenInput=this.token; this.login(); }
      },
      icons() { window.lucide?.createIcons({attrs:{'aria-hidden':'true'},nameAttr:'data-lucide'}); },
      toggleTheme() { this.theme=this.theme==='dark'?'light':'dark'; },
      number(n) { return Number(n||0).toLocaleString('en-US'); },
      time(value) {
        if(!value) return '—';
        const d=typeof value==='number'?new Date(value*1000):new Date(value);
        if(!Number.isFinite(d.getTime())||d.getFullYear()<2000) return '—';
        return d.toLocaleTimeString('zh-CN',{hour12:false});
      },
      duration(n) { n=Number(n||0);return n>=3600?Math.floor(n/3600)+' 小时 '+Math.floor(n%3600/60)+' 分':Math.floor(n/60)+' 分钟'; },
      phaseLabel(p) { return ({idle:'空闲',preparing:'准备上下文',requesting:'请求模型',tools:'执行工具',summarizing:'压缩上下文',error:'上次活动失败',disabled:'未启用',uninitialized:'尚未初始化'})[p]||'等待状态'; },
      kindLabel(k) { return kinds[k]||k||'—'; },
      kindClass(k) { return ['input','tool_call','tool_definition','reply'].includes(k)?'info':['summary','reasoning'].includes(k)?'warning':k==='error'?'danger':['output','finish','tool_result'].includes(k)?'success':''; },
      chatName(c) { return c.group_name||c.nickname||(c.chat_type==='group'?'群聊 ':'私聊 ')+c.chat_id; },
      hitRate() { const u=this.overview.usage||{},n=(u.cache_hit_tokens||0)+(u.cache_miss_tokens||0);return n?Math.round(u.cache_hit_tokens/n*100):0; },
      budgetPercent() { return Math.min(100,Math.max(0,(this.runtime.metrics?.estimated_tokens||0)/(this.runtime.metrics?.budget||1)*100)); },
      notify(text) { this.toast=text;clearTimeout(transport.toastTimer);transport.toastTimer=setTimeout(()=>{this.toast='';},3500); },
      async requestJSON(path,options={}) {
        const generation=transport.generation,controller=new AbortController();
        transport.controllers.add(controller);
        try {
          const headers={Authorization:'Bearer '+this.token,...options.headers};
          if(options.body && !headers['Content-Type']) headers['Content-Type']='application/json';
          const response=await fetch(path,{...options,headers,signal:controller.signal,cache:'no-store'});
          const text=await response.text();
          if(generation!==transport.generation) throw new DOMException('Stale authentication','AbortError');
          let body;try{body=JSON.parse(text);}catch{body={error:text.trim()};}
          if(!response.ok) {
            const err=new Error(body.error||'请求失败 '+response.status);err.status=response.status;err.code=body.code;
            if(response.status===401 && this.authenticated) this.clearSession();
            throw err;
          }
          return body;
        } finally {transport.controllers.delete(controller);}
      },
      async login() {
        if(this.busy.login) return;
        this.busy.login=true;this.loginError='';this.token=this.tokenInput.trim();
        const generation=transport.generation;
        try {
          const runtime=await this.requestJSON('/api/runtime');
          if(generation!==transport.generation) return;
          this.runtime=runtime;this.authenticated=true;localStorage.setItem('qmi_token',this.token);
          location.hash=this.page;await this.refreshPage();
        } catch(e) {if(e.name!=='AbortError')this.loginError=e.status===401?'访问令牌不正确':e.message;}
        finally{this.busy.login=false;}
      },
      clearSession() {
        transport.generation++;transport.chatGeneration++;transport.contextGeneration++;transport.promptGeneration++;
        this.stopPageRefresh();for(const c of transport.controllers)c.abort();transport.controllers.clear();
        localStorage.removeItem('qmi_token');this.authenticated=false;this.token='';this.tokenInput='';
        this.runtime={};this.overview={};this.events=[];this.afterID=0;this.chats=[];this.messages=[];this.selectedChat=null;this.searchResults=null;this.contextPage={entries:[]};this.contextDetail=null;this.contextConflict='';
        this.notes=[];this.tasks=[];this.timers=[];this.promptDraft='';this.promptBaseline='';this.promptLoaded=false;this.promptActiveName='';this.promptPath='';
        this.moodDraft='';this.moodBaseline='';this.noteDraft='';this.wakeDraft='';this.timerNote='';
        this.groups=[];this.friends=[];this.memberData={};this.memberGroup=null;this.napResult='';this.napParams='{}';this.napConfirmed=false;
        this.configView={};this.configLoaded=false;this.configReview=null;
        this.configDraft={models:emptyAgent(),behavior:emptyAgent(),modules:emptyAgent()};
        this.configBaseline={models:emptyAgent(),behavior:emptyAgent(),modules:emptyAgent()};
        this.errors={};this.lastSuccess={};this.busy=emptyBusy();this.drawerOpen=false;this.toast='';
      },
      logout() { if(this.anyDirty()&&!confirm('有未保存的更改，确认退出并丢弃？'))return;this.clearSession(); },
      anyDirty() { return this.promptDirty||this.moodDraft!==this.moodBaseline||['models','behavior','modules'].some(s=>this.configDirty(s)); },
      navigate(next,fromHash=false) {
        if(!pages[next])return;
        if(next!==this.page && ((this.page==='config'&&['models','behavior','modules'].some(s=>this.configDirty(s)))||(this.page==='agent'&&this.promptDirty))) {
          if(!confirm('有未保存的更改，仍要离开？草稿会保留。')){if(fromHash)location.hash=this.page;return;}
        }
        this.stopPageRefresh();this.page=next;this.drawerOpen=false;this.contextDetail=null;
        if(location.hash!=='#'+next)location.hash=next;
        this.refreshPage();
      },
      stopPageRefresh() {clearTimeout(transport.timer);transport.timer=null;},
      startPageRefresh() {
        this.stopPageRefresh();
        if(!this.authenticated||document.hidden)return;
        if(['overview','context','messages','activity'].includes(this.page)||(this.page==='agent'&&this.agentTab==='tasks'))
          transport.timer=setTimeout(()=>this.refreshPage(),3000);
      },
      async refreshPage(manual=false) {
        if(!this.authenticated||transport.refreshing)return;
        transport.refreshing=true;this.busy.refresh=true;
        const page=this.page,generation=transport.generation;
        try {
          if(page==='overview') {
            const [overview,runtime]=await Promise.all([this.requestJSON('/api/overview'),this.requestJSON('/api/runtime')]);
            this.overview=overview;this.runtime=runtime;await this.loadEvents();
          } else if(page==='context') {
            this.runtime=await this.requestJSON('/api/runtime');
            if(!this.chats.length)this.chats=await this.requestJSON('/api/chats');
            if(!this.contextDetail)await this.loadContext(false,true);
            else if((this.contextQuery.source==='request'?this.runtime.request_version:this.runtime.working_version)!==this.contextPage.version)this.contextConflict='已有更新的快照；当前详情保持不变。';
          } else if(page==='activity') await this.loadEvents();
          else if(page==='messages')this.chats=await this.requestJSON('/api/chats');
          else if(page==='agent') {
            if(!this.notes.length||manual) {
              const [notes,mood,timers]=await Promise.all([this.requestJSON('/api/notes'),this.requestJSON('/api/mood'),this.requestJSON('/api/timers')]);
              this.notes=notes;this.timers=timers;
              if(this.moodDraft===this.moodBaseline){this.moodDraft=mood.mood||'';this.moodBaseline=this.moodDraft;}
            }
            if(this.agentTab==='prompts')await this.loadPrompt();
            if(this.agentTab==='tasks')await this.loadTasks();
          } else if(page==='qq')await this.loadQQ();
          else if(page==='config'&&!this.configLoaded)await this.loadConfig();
          if(generation===transport.generation){this.connection='connected';this.errors[page]='';this.lastSuccess[page]=new Date().toISOString();}
        } catch(e) {if(e.name!=='AbortError'&&generation===transport.generation){this.errors[page]=e.message;this.connection='error';}}
        finally {transport.refreshing=false;this.busy.refresh=false;this.$nextTick(()=>this.icons());if(this.page!==page&&this.authenticated)this.refreshPage();else this.startPageRefresh();}
      },
      async guarded(key,fn,message) {
        if(this.busy[key])return;
        this.busy[key]=true;const page=this.page,generation=transport.generation;
        try {await fn();if(generation===transport.generation){this.errors[page]='';if(message)this.notify(message);}}
        catch(e){if(e.name!=='AbortError'&&generation===transport.generation)this.errors[page]=e.message;}
        finally{if(generation===transport.generation)this.busy[key]=false;}
      },
      async loadContext(reset=false,propagate=false) {
        if(reset)this.contextQuery.offset=0;
        const generation=++transport.contextGeneration,q=this.contextQuery;
        const params=new URLSearchParams({source:q.source,kind:q.kind,q:q.q,offset:q.offset,limit:40});
        if(q.scope){const [type,id]=q.scope.split(':');params.set('chat_type',type);params.set('chat_id',id);}
        try {
          const data=await this.requestJSON('/api/context?'+params);
          if(generation!==transport.contextGeneration)return;
          this.contextPage=data;this.contextConflict='';this.errors.context='';
        } catch(e){if(propagate)throw e;if(e.name!=='AbortError')this.errors.context=e.message;}
      },
      resetContext() { this.contextQuery={source:'working',kind:'',scope:'',q:'',offset:0};this.loadContext(true); },
      async openContextEntry(entry) {
        await this.guarded('detail',async()=> {
          const version=this.contextPage.version,source=this.contextPage.source;
          try {
            const data=await this.requestJSON('/api/context/message?'+new URLSearchParams({source,version,index:entry.index}));
            if(this.page==='context'){this.contextDetail=data;this.focusDialog('上下文详情');}
          } catch(e) {if(e.status===409){this.contextConflict=e.message;this.contextDetail=null;return;}throw e;}
        });
      },
      async copyText(text) {
        try {
          if(navigator.clipboard?.writeText)await navigator.clipboard.writeText(text);
          else {const el=document.createElement('textarea');el.value=text;el.style.position='fixed';el.style.opacity='0';document.body.append(el);el.select();if(!document.execCommand('copy'))throw new Error();el.remove();}
          this.notify('已复制');
        } catch {this.notify('复制失败，请使用下载或手动选择文本');}
      },
      copyContextEntry() { if(this.contextDetail)this.copyText(JSON.stringify({display_only:true,source:this.contextPage.source,version:this.contextPage.version,entry:this.contextDetail},null,2)); },
      downloadContextPage() {
        const blob=new Blob([JSON.stringify({display_only:true,bounded:true,...clone(this.contextPage)},null,2)],{type:'application/json'});
        const url=URL.createObjectURL(blob),a=document.createElement('a');a.href=url;a.download='qmi-context-display-v'+(this.contextPage.version||0)+'.json';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
      },
      async loadEvents() {
        const rows=await this.requestJSON('/api/events?after='+this.afterID);
        const seen=new Set(this.events.map(e=>e.id)),fresh=rows.filter(e=>!seen.has(e.id));
        this.events=[...this.events,...fresh].slice(-500);
        for(const e of rows)this.afterID=Math.max(this.afterID,e.id);
        if(!this.followEvents)this.newEventCount+=fresh.length;
        else if(this.page==='activity')await this.scrollLogs();
      },
      async scrollLogs() {await this.$nextTick();if(this.$refs.activityBox)this.$refs.activityBox.scrollTop=this.$refs.activityBox.scrollHeight;},
      toggleFollow() {this.followEvents=!this.followEvents;if(this.followEvents){this.newEventCount=0;this.scrollLogs();}},
      async selectChat(chat,anchor='') {
        const generation=++transport.chatGeneration;
        this.selectedChat=chat;this.messages=[];this.highlightMessageID=anchor;this.searchScope='current';
        const params=new URLSearchParams({chat_type:chat.chat_type,chat_id:chat.chat_id,limit:50});
        if(anchor)params.set('around_message_id',anchor);
        this.busy.messages=true;
        try {
          const rows=await this.requestJSON('/api/messages?'+params);
          if(generation!==transport.chatGeneration)return;
          this.messages=rows.slice().reverse();this.hasOlder=rows.length>=50||!!anchor;
          await this.$nextTick();
          if(anchor)this.$refs.messageBox.querySelector('[data-message-id="'+CSS.escape(String(anchor))+'"]')?.scrollIntoView({block:'center'});
          else this.$refs.messageBox.scrollTop=this.$refs.messageBox.scrollHeight;
        }catch(e){if(e.name!=='AbortError'&&generation===transport.chatGeneration)this.errors.messages=e.message;}
        finally{if(generation===transport.chatGeneration)this.busy.messages=false;}
      },
      async loadOlderMessages() {
        if(this.busy.messages||!this.selectedChat||!this.messages.length)return;
        await this.guarded('messages',async()=>{
          const generation=transport.chatGeneration,box=this.$refs.messageBox,height=box.scrollHeight,top=box.scrollTop;
          const rows=await this.requestJSON('/api/messages?'+new URLSearchParams({chat_type:this.selectedChat.chat_type,chat_id:this.selectedChat.chat_id,limit:50,before_time:this.messages[0].time}));
          if(generation!==transport.chatGeneration)return;
          const ids=new Set(this.messages.map(m=>String(m.message_id)));
          this.messages=[...rows.filter(m=>!ids.has(String(m.message_id))).reverse(),...this.messages];this.hasOlder=rows.length>=50;
          await this.$nextTick();box.scrollTop=top+box.scrollHeight-height;
        });
      },
      async searchMessages() {
        if(!this.messageSearch.trim())return;
        await this.guarded('search',async()=>{
          const body={query:this.messageSearch,limit:100};
          if(this.searchScope==='current'&&this.selectedChat){body.chat_type=this.selectedChat.chat_type;body.chat_id=Number(this.selectedChat.chat_id);}
          this.searchResults=await this.requestJSON('/api/search',{method:'POST',body:JSON.stringify(body)});
        });
      },
      openSearchHit(m) {this.searchResults=null;this.selectChat({chat_type:m.chat_type,chat_id:m.chat_id},m.message_id);},
      saveMood() {return this.guarded('mood',async()=>{const mood=this.moodDraft;await this.requestJSON('/api/mood',{method:'POST',body:JSON.stringify({mood})});this.moodBaseline=mood;},'情绪已保存');},
      addNote() {if(!this.noteDraft.trim())return;return this.guarded('note',async()=>{await this.requestJSON('/api/notes',{method:'POST',body:JSON.stringify({text:this.noteDraft})});this.noteDraft='';this.notes=await this.requestJSON('/api/notes');},'笔记已添加');},
      deleteNote(n) {if(!confirm('确认删除这条笔记？'))return;return this.guarded('note',async()=>{await this.requestJSON('/api/notes/'+n.index,{method:'DELETE'});this.notes=await this.requestJSON('/api/notes');},'笔记已删除');},
      wakeAgent() {if(!this.wakeDraft.trim())return;return this.guarded('wake',async()=>{await this.requestJSON('/api/wake',{method:'POST',body:JSON.stringify({text:this.wakeDraft})});this.wakeDraft='';},'唤醒信息已入队');},
      addTimer() {return this.guarded('timer',async()=>{await this.requestJSON('/api/timers',{method:'POST',body:JSON.stringify({delay_seconds:this.timerDelay,note:this.timerNote})});this.timerNote='';this.timers=await this.requestJSON('/api/timers');},'计时器已添加');},
      cancelTimer(t) {if(!confirm('确认取消此计时器？'))return;return this.guarded('timer',async()=>{await this.requestJSON('/api/timers/'+encodeURIComponent(t.id),{method:'DELETE'});this.timers=await this.requestJSON('/api/timers');},'计时器已取消');},
      async loadPrompt() {
        if(this.promptName===this.promptActiveName&&this.promptLoaded)return;
        if(this.promptDirty&&!confirm('切换文件将丢弃当前提示词草稿，继续？')){this.promptName=this.promptActiveName;return;}
        const generation=++transport.promptGeneration,name=this.promptName;this.promptLoaded=false;
        await this.guarded('prompt',async()=>{
          const data=await this.requestJSON('/api/file?name='+name);
          if(generation!==transport.promptGeneration)return;
          this.promptActiveName=name;this.promptDraft=data.content;this.promptBaseline=data.content;this.promptPath=data.path||name;this.promptLoaded=true;
        });
      },
      savePrompt() {
        if(!this.promptLoaded)return;
        return this.guarded('prompt',async()=>{const content=this.promptDraft;await this.requestJSON('/api/file',{method:'PUT',body:JSON.stringify({name:this.promptActiveName,content})});this.promptBaseline=content;},'提示词已保存，下一次请求读取');
      },
      async loadTasks() {this.tasks=await this.requestJSON('/api/tasks?limit=100');this.startPageRefresh();},
      async loadQQ() {
        // Independent results allow a healthy list to remain visible if another fails.
        const results=await Promise.allSettled([this.requestJSON('/api/groups'),this.requestJSON('/api/friends'),this.requestJSON('/api/napcat')]);
        if(results[0].status==='fulfilled')this.groups=results[0].value;
        if(results[1].status==='fulfilled')this.friends=results[1].value;
        if(results[2].status==='fulfilled'){this.napActions=results[2].value.actions||[];this.napDangerous=results[2].value.dangerous||[];}
        const failed=results.filter(r=>r.status==='rejected');if(failed.length)throw failed[0].reason;
      },
      loadMembers(g) {return this.guarded('members',async()=>{const data=await this.requestJSON('/api/members?group_id='+g.group_id);this.memberGroup=g;this.memberData=data;});},
      runNapcat() {
        return this.guarded('napcat',async()=>{
          let params;try{params=JSON.parse(this.napParams);}catch{throw new Error('参数不是有效 JSON');}
          if(!params||Array.isArray(params)||typeof params!=='object')throw new Error('参数必须是 JSON 对象');
          if(this.napDangerous.includes(this.napAction)){if(!this.napConfirmed)throw new Error('请显式确认危险操作');params.confirm=true;}
          const out=await this.requestJSON('/api/napcat',{method:'POST',body:JSON.stringify({action:this.napAction,params})});
          this.napResult=JSON.stringify(out,null,2);this.napConfirmed=false;
        },'动作已执行');
      },
      normalizeAgent(view) {
        const a=clone(view.effective?.agent||{});
        a.models??={};a.wake_routes??={chat:'chat',background:'background'};a.modules??={};a.history??={max:100,trim:40};
        a.context={max_input_tokens:64000,review_after_wakes:8,keep_recent_messages:12,summary_tokens:1024,...a.context};
        a.multimodal=a.multimodal!==false;a.debug=!!a.debug;
        if(typeof a.debounce==='number')a.debounce=(a.debounce/1e9)+'s';
        return a;
      },
      async loadConfig() {
        const view=await this.requestJSON('/api/config');this.configView=view;
        const a=this.normalizeAgent(view);
        for(const s of ['models','behavior','modules']){this.configBaseline[s]=clone(a);this.configDraft[s]=clone(a);}
        this.moduleNames=[...new Set([...this.moduleNames,...Object.keys(a.modules)])];this.configLoaded=true;
      },
      configDirty(section) {return this.configLoaded&&qmiConfigEditor.hasChanges(section,this.configBaseline[section]||{},this.configDraft[section]||{});},
      tierCandidates(tier) {return Array.isArray(tier.candidates)?tier.candidates:[tier];},
      toggleCapability(c,cap,on) {const values=new Set(c.capabilities||[]);on?values.add(cap):values.delete(cap);c.capabilities=[...values];},
      addCandidate(name) {
        const tier=this.configDraft.models.models[name];
        if(!Array.isArray(tier.candidates)){if(tier.model&&!confirm('将旧单模型档位转为候选列表？'))return;tier.candidates=tier.model?[{name,...clone(tier)}]:[];}
        tier.candidates.push({name:'',model:'',base_url:'',api_key:'',capabilities:['tools'],enable:true});
      },
      addTier() {
        const name=this.newTier.trim();if(!name)return;
        if(this.configDraft.models.models[name]){this.notify('档位已存在');return;}
        this.configDraft.models.models[name]={strategy:'priority',candidates:[]};this.newTier='';this.addCandidate(name);
      },
      moduleLabel(name) {return ({core:'发消息、笔记、情绪与结束活动',memory:'历史查询、图片与会话勿扰',sticker:'收藏表情与 QQ 表情',qzone:'QQ 空间浏览与互动',admin:'群聊、好友和 QQ 管理',computer:'工作区、文件与命令执行',browser:'网页浏览与截图',system:'计时器、后台任务与行为回溯'})[name]||'工具板块';},
      discardConfig(section) {if(this.configDirty(section)&&confirm('丢弃这个分区的变更？'))this.configDraft[section]=clone(this.configBaseline[section]);},
      async reviewConfig(section) {
        await this.guarded('config',async()=>{
          const patch=qmiConfigEditor.buildPatch(section,this.configBaseline[section],this.configDraft[section]);
          if(!Object.keys(patch.agent).length)return;
          if(section==='models'){
            for(const [name,tier] of Object.entries(patch.agent.models||{})){
              const names=new Set();
              for(const c of tier.candidates||[]){if(!c.name?.trim()||!c.model?.trim())throw new Error(name+' 的候选名称和模型不能为空');if(names.has(c.name))throw new Error('候选名重复：'+c.name);names.add(c.name);}
            }
          }
          await this.requestJSON('/api/config/validate',{method:'POST',body:JSON.stringify(patch)});
          this.configReview={section,patch,diff:qmiConfigEditor.diffFields({agent:this.configBaseline[section]},patch)};
          this.focusDialog('确认配置变更');
        });
      },
      async commitConfig() {
        if(!this.configReview)return;
        await this.guarded('config',async()=>{
          const review=clone(this.configReview),view=await this.requestJSON('/api/config',{method:'PUT',body:JSON.stringify(review.patch)});
          this.configView=view;const a=this.normalizeAgent(view);
          this.configBaseline[review.section]=clone(a);this.configDraft[review.section]=clone(a);this.configReview=null;
        },'配置已保存并应用');
      },
      reloadConfig() {
        if(this.anyDirty()&&!confirm('重载会丢弃配置草稿，继续？'))return;
        return this.guarded('config',async()=>{await this.requestJSON('/api/config/reload',{method:'POST'});await this.loadConfig();},'已重载配置文件');
      },
      testModel(tier,name) {
        if(this.configDirty('models'))return;
        return this.guarded('modelTest',async()=>{const data=await this.requestJSON('/api/models/test',{method:'POST',body:JSON.stringify({tier,name})});if(!data.ok)throw new Error(data.error||'模型不可用');this.notify(name+' 可用 · '+data.latency_ms+' ms');});
      },
      closeOverlays() {this.drawerOpen=false;this.contextDetail=null;if(!this.busy.config)this.configReview=null;},
      focusDialog(name) {this.$nextTick(()=>document.querySelector('[aria-label="'+name+'"] button')?.focus());},
      trapFocus(e) {
        if(e.key!=='Tab'||(!this.contextDetail&&!this.configReview&&!this.drawerOpen))return;
        const root=this.drawerOpen?document.querySelector('.sidebar'):document.querySelector('[role="dialog"][aria-label="'+(this.contextDetail?'上下文详情':'确认配置变更')+'"]');
        const nodes=[...root.querySelectorAll('button:not(:disabled),input,select,textarea,summary,[tabindex="0"]')].filter(n=>n.getClientRects().length);
        if(!nodes.length)return;
        if(e.shiftKey&&document.activeElement===nodes[0]){e.preventDefault();nodes.at(-1).focus();}
        else if(!e.shiftKey&&document.activeElement===nodes.at(-1)){e.preventDefault();nodes[0].focus();}
      },
    };
  };
})();
