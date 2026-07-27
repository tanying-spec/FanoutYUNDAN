package main

import "net/http"

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}

const indexHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>FanoutYUNDAN</title>
<style>
:root{
  --bg:#12151a; --panel:#181c23; --line:#262c36; --text:#dde3ec;
  --dim:#8b95a5; --accent:#4a9eda; --ok:#3fa66b; --warn:#c9903a; --bad:#c25450;
}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--text);
  font:13px/1.5 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
header{display:flex;align-items:center;gap:16px;padding:10px 16px;
  border-bottom:1px solid var(--line);background:var(--panel)}
h1{font-size:13px;font-weight:600;margin:0;letter-spacing:0}
.spacer{flex:1}
button{font:inherit;color:var(--text);background:#222833;border:1px solid var(--line);
  border-radius:4px;padding:4px 10px;cursor:pointer}
button:hover{border-color:var(--accent)}
button:disabled{opacity:.45;cursor:default}
button.primary{background:var(--accent);border-color:var(--accent);color:#0b0e12;font-weight:600}
.wrap{display:grid;grid-template-columns:1fr 1fr;gap:1px;background:var(--line);
  height:calc(100vh - 41px)}
section{background:var(--bg);display:flex;flex-direction:column;min-height:0}
.head{display:flex;align-items:center;gap:10px;padding:8px 12px;
  border-bottom:1px solid var(--line);background:var(--panel)}
.head h2{font-size:12px;margin:0;font-weight:600}
.count{color:var(--dim);font-size:11px}
.scroll{overflow:auto;flex:1}
table{width:100%;border-collapse:collapse}
th{position:sticky;top:0;background:var(--panel);text-align:left;font-weight:600;
  color:var(--dim);font-size:11px;padding:6px 10px;border-bottom:1px solid var(--line);
  white-space:nowrap}
td{padding:5px 10px;border-bottom:1px solid #1e232b;white-space:nowrap}
tr:hover td{background:#1a1f27}
.num{text-align:right;font-variant-numeric:tabular-nums}
.dim{color:var(--dim)}
.tag{display:inline-block;padding:1px 6px;border-radius:3px;font-size:11px}
.s-up{background:rgba(63,166,107,.16);color:var(--ok)}
.s-starting{background:rgba(201,144,58,.16);color:var(--warn)}
.s-failed{background:rgba(194,84,80,.16);color:var(--bad)}
.port{color:var(--accent);font-weight:600}
.empty{padding:24px 12px;color:var(--dim);text-align:center}
.notice{margin:10px 12px;padding:8px 10px;border:1px solid rgba(194,84,80,.5);
  background:rgba(194,84,80,.1);color:#e09a96;border-radius:4px;display:none}
input[type=search]{font:inherit;background:#0e1116;border:1px solid var(--line);
  color:var(--text);border-radius:4px;padding:4px 8px;width:150px}
input[type=search]:focus{outline:none;border-color:var(--accent)}
.form{display:grid;grid-template-columns:130px minmax(0,1fr);gap:10px 14px;padding:14px}
.form label{color:var(--dim);align-self:center}
.form input,.form select{font:inherit;background:#0e1116;border:1px solid var(--line);
  color:var(--text);border-radius:4px;padding:6px 8px;width:100%;max-width:none}
.form input:focus,.form select:focus{outline:none;border-color:var(--accent)}
.actions{display:flex;justify-content:flex-end;gap:8px;padding:10px 14px;border-top:1px solid var(--line)}
.err{color:var(--bad);font-size:11px;max-width:260px;overflow:hidden;
  text-overflow:ellipsis;display:inline-block;vertical-align:bottom}
.links{display:flex;gap:14px;margin-right:4px}
.links a{color:var(--dim);text-decoration:none;font-size:12px}
.links a:hover{color:var(--accent)}
@media(max-width:760px){.links{display:none}}
@media(max-width:860px){.wrap{grid-template-columns:1fr;height:auto}}
select{font:inherit;background:#0e1116;border:1px solid var(--line);color:var(--text);
  border-radius:4px;padding:3px 6px;max-width:150px}
select:focus{outline:none;border-color:var(--accent)}
.modal{position:fixed;inset:0;background:rgba(8,10,14,.72);display:none;
  align-items:center;justify-content:center;z-index:50}
.modal.open{display:flex}
.sheet{background:var(--bg);border:1px solid var(--line);border-radius:6px;
  width:min(760px,92vw);max-height:82vh;display:flex;flex-direction:column}
.sheet .head{border-radius:6px 6px 0 0}
.sheet .scroll{max-height:64vh}
a.lnk{color:var(--text);text-decoration:none;border-bottom:1px dotted var(--dim)}
a.lnk:hover{color:var(--accent);border-color:var(--accent)}
.kv{display:grid;grid-template-columns:88px 1fr;gap:4px 12px;padding:12px 14px}
.kv dt{color:var(--dim)}
.kv dd{margin:0;word-break:break-all}
.share{margin:0 14px 14px;padding:10px;background:#0e1116;border:1px solid var(--line);
  border-radius:4px;word-break:break-all;font-size:12px;line-height:1.7}
.share button{margin-top:8px}
#exbox{width:100%;min-height:320px;background:#0e1116;border:0;color:var(--text);
  font:12px/1.8 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;
  padding:12px 14px;resize:vertical}
#exbox:focus{outline:none}
</style>
</head>
<body>
<header>
  <h1>FanoutYUNDAN</h1>
  <span class="count" id="mihomo-status">正在检测 Mihomo…</span>
  <span class="spacer"></span>
  <nav class="links">
    <a href="https://github.com/tanying-spec/FanoutYUNDAN" target="_blank" rel="noopener" title="源码与问题反馈">GitHub</a>
  </nav>
  <button id="refresh">重新拉取节点</button>
</header>

<div class="wrap">
  <section>
    <div class="head">
      <h2>运行中</h2>
      <span class="count" id="tcount"></span>
      <span class="spacer"></span>
      <button class="primary" id="addnode">添加节点</button>
      <button id="stopall">全部停止</button>
    </div>
    <div class="scroll">
      <table>
        <thead><tr>
          <th style="width:28px"><input type="checkbox" id="checkall" title="全选已连通"></th>
          <th>端口</th><th>节点</th><th>地区</th><th>出口 IP</th><th>状态</th><th></th>
        </tr></thead>
        <tbody id="tbody"></tbody>
      </table>
      <div class="empty" id="tempty">还没有运行中的隧道，从右侧选一个节点启动</div>
    </div>
  </section>

  <section>
    <div class="head">
      <h2>Mihomo 入站</h2>
      <span class="count" id="icount"></span>
      <span class="spacer"></span>
      <button id="exportBtn" title="导出勾选入站的分享链接">导出链接</button>
      <button class="primary" id="createMihomo">从选中出口创建</button>
      <button id="reloadin">刷新</button>
    </div>
    <div class="scroll">
      <table>
        <thead><tr>
          <th>名称</th><th>协议</th><th>SOCKS 端口</th><th>操作</th>
        </tr></thead>
        <tbody id="ibody"></tbody>
      </table>
      <div class="empty" id="iempty">还没有 Mihomo 入站。先在左侧勾选一个已连接出口。</div>
    </div>
  </section>
</div>

<div class="modal" id="nodemodal">
  <div class="sheet">
    <div class="head">
      <h2>选择节点启动</h2>
      <span class="count" id="ncount"></span>
      <span class="spacer"></span>
      <input type="search" id="filter" placeholder="按地区/主机名筛选">
      <button id="closemodal">关闭</button>
    </div>
    <div class="notice" id="nerror"></div>
    <div class="scroll">
      <table>
        <thead><tr>
          <th>主机名</th><th>地区</th><th class="num">延迟</th>
          <th class="num">带宽</th><th class="num">会话</th><th></th>
        </tr></thead>
        <tbody id="nbody"></tbody>
      </table>
    </div>
  </div>
</div>

<div class="modal" id="exportmodal">
  <div class="sheet">
    <div class="head">
      <h2>导出链接</h2>
      <span class="count" id="excount"></span>
      <span class="spacer"></span>
      <button id="copyall">全部复制</button>
      <button id="closeexport">关闭</button>
    </div>
    <div class="scroll"><textarea id="exbox" spellcheck="false"></textarea></div>
  </div>
</div>

<div class="modal" id="mihomomodal">
  <div class="sheet" style="width:min(620px,92vw)">
    <div class="head">
      <h2>创建 Mihomo 入站</h2>
      <span class="count" id="mselected"></span>
      <span class="spacer"></span>
      <button id="closemihomo">关闭</button>
    </div>
    <div class="scroll">
      <div class="form">
        <label for="mtemplate">入站模板</label><select id="mtemplate"></select>
        <label for="mmode">入口模式</label><select id="mmode"><option value="direct">直连 WS</option><option value="cdn">Cloudflare CDN WS-TLS</option><option value="argo">Cloudflare Argo WS-TLS</option><option value="reality">VLESS Reality</option></select>
        <label for="maddress">公网地址</label><input id="maddress" type="text" autocomplete="off">
        <label for="mport">公网端口</label><input id="mport" type="number" min="1" max="65535">
        <label for="mhost">WS Host</label><input id="mhost" type="text" autocomplete="off">
        <label for="msni">SNI</label><input id="msni" type="text" autocomplete="off">
        <label for="mpath">WS Path</label><input id="mpath" type="text" autocomplete="off">
        <label for="mpublickey">Reality 公钥</label><input id="mpublickey" type="text" autocomplete="off">
        <label for="mshortid">Reality Short ID</label><input id="mshortid" type="text" autocomplete="off">
      </div>
    </div>
    <div class="actions"><button class="primary" id="confirmMihomo">创建</button></div>
  </div>
</div>

<script>
const $ = s => document.querySelector(s);
let nodes = [], tunnels = [], maxSlots = 1, nodeError = '', mihomoTemplates = [];
const picked = new Set();

// 界面挂在随机前缀下，请求一律走相对路径，去掉开头的斜杠即可
async function api(path, opts){
  const r = await fetch(path.replace(/^\//, ''), opts);
  const d = await r.json().catch(()=>({}));
  if(!r.ok) throw new Error(d.error || ('HTTP '+r.status));
  return d;
}

function statusTag(s){
  const label = {up:'已连通', starting:'连接中', failed:'失败', stopped:'已停止'}[s] || s;
  return '<span class="tag s-'+s+'">'+label+'</span>';
}

function renderTunnels(){
  const tb = $('#tbody');
  // 隧道停掉后把它的勾选也去掉，避免复制到已经不存在的出口
  const alive = new Set(tunnels.filter(t => t.status === 'up').map(t => t.slot));
  for(const s of [...picked]) if(!alive.has(s)) picked.delete(s);
  $('#tempty').style.display = tunnels.length ? 'none' : '';
  updatePickCount();
  tb.innerHTML = tunnels.map(t => {
    const detail = t.status === 'failed' && t.err
      ? '<span class="err" title="'+esc(t.err)+'">'+esc(t.err)+'</span>' : '';
    const sel = t.status === 'up'
      ? '<input type="checkbox" class="pick" value="'+t.slot+'"'+(picked.has(t.slot)?' checked':'')+'>'
      : '';
    return '<tr>'
      + '<td>'+sel+'</td>'
      + '<td class="port">'+t.port+'</td>'
      + '<td>'+esc(t.node.hostname)+'</td>'
      + '<td class="dim">'+esc(t.node.country_code)+'</td>'
      + '<td>'+(t.exit_ip || '<span class="dim">—</span>')+'</td>'
      + '<td>'+statusTag(t.status)+' '+detail+'</td>'
      + '<td><button data-stop="'+t.slot+'">停止</button></td>'
      + '</tr>';
  }).join('');
}

function renderNodes(){
  const kw = $('#filter').value.trim().toLowerCase();
  const running = new Set(tunnels.map(t => t.node.hostname));
  const list = nodes.filter(n => !kw
    || n.hostname.toLowerCase().includes(kw)
    || n.country.toLowerCase().includes(kw)
    || n.country_code.toLowerCase().includes(kw));
  $('#ncount').textContent = list.length + ' 个';
  const notice = $('#nerror');
  notice.style.display = nodeError ? 'block' : 'none';
  notice.textContent = nodeError ? '节点列表获取失败：' + nodeError + '。系统会自动重试，也可以点击顶部“重新拉取节点”。' : '';
  $('#nbody').innerHTML = list.slice(0,150).map(n => {
    const busy = running.has(n.hostname);
	const switchSlot = !busy && tunnels.length >= maxSlots && maxSlots === 1 ? tunnels[0].slot : 0;
    return '<tr>'
      + '<td>'+esc(n.hostname)+'</td>'
      + '<td class="dim">'+esc(n.country_code)+' '+esc(n.country)+'</td>'
      + '<td class="num">'+n.ping+' ms</td>'
      + '<td class="num">'+n.speed_mbps.toFixed(0)+' Mbps</td>'
      + '<td class="num dim">'+n.sessions+'</td>'
	  + '<td><button '+(switchSlot ? 'data-switch="'+switchSlot+'" ' : '')
	  + 'data-host="'+esc(n.hostname)+'"'+(busy?' disabled':'')+'>'
	  + (busy?'已启动':(switchSlot?'切换':'启动'))+'</button></td>'
      + '</tr>';
  }).join('');
}

function esc(s){ return String(s).replace(/[&<>"']/g, c =>
  ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }

async function poll(){
  try{
    tunnels = await api('/api/tunnels') || [];
    renderTunnels(); renderNodes();
    if(mihomoInbounds.length) loadMihomo();
  }catch(e){}
}

async function loadNodes(){
  const d = await api('/api/nodes');
  nodes = d.nodes || [];
	nodeError = d.error || '';
	maxSlots = d.max_slots || 1;
  renderNodes();
}

document.addEventListener('click', async e => {
	const host = e.target.dataset.host, switchSlot = e.target.dataset.switch, stop = e.target.dataset.stop;
	if(host){
	  e.target.disabled = true; e.target.textContent = switchSlot ? '切换中' : '启动中';
	  const path = switchSlot
	    ? '/api/switch?slot='+switchSlot+'&host='+encodeURIComponent(host)
	    : '/api/start?host='+encodeURIComponent(host);
	  try{ await api(path, {method:'POST'}); }
	  catch(err){ alert((switchSlot?'切换':'启动')+'失败: '+err.message); }
    poll();
  }
  if(stop){
    e.target.disabled = true;
    try{ await api('/api/stop?slot='+stop, {method:'POST'}); }
    catch(err){ alert('停止失败: '+err.message); }
    poll();
  }
});

$('#refresh').onclick = async e => {
  e.target.disabled = true; e.target.textContent = '拉取中';
  try{ await api('/api/refresh', {method:'POST'}); await loadNodes(); }
  catch(err){ alert('拉取失败: '+err.message); }
  e.target.disabled = false; e.target.textContent = '重新拉取节点';
};

$('#stopall').onclick = async () => {
  for(const t of tunnels){
    try{ await api('/api/stop?slot='+t.slot, {method:'POST'}); }catch(e){}
  }
  poll();
};

$('#filter').oninput = renderNodes;

let mihomoInbounds = [];

async function loadMihomo(){
  try {
    mihomoInbounds = await api('/api/mihomo/inbounds') || [];
    $('#mihomo-status').textContent = 'Mihomo 入站 ' + mihomoInbounds.length + ' 个';
    $('#icount').textContent = mihomoInbounds.length ? mihomoInbounds.length + ' 个' : '';
    $('#iempty').style.display = mihomoInbounds.length ? 'none' : '';
    $('#ibody').innerHTML = mihomoInbounds.map(i => {
      const options = tunnels.filter(t => t.status === 'up').map(t => '<option value="' + t.port + '"' + (t.port === i.port ? ' selected' : '') + '>' + esc(t.node.country_code) + ' · ' + t.port + ' · ' + esc(t.exit_ip || t.node.hostname) + '</option>').join('');
      return '<tr>'
      + '<td>' + esc(i.name) + '</td><td class="dim">' + esc((i.protocol || 'vless-ws') + ' / ' + (i.mode || 'direct')) + '</td>'
      + '<td><select data-bind-mihomo="' + esc(i.name) + '">' + options + '</select></td><td>'
      + '<button data-copy="' + esc(i.link) + '">复制链接</button> '
      + '<button data-delete-mihomo="' + esc(i.name) + '">删除</button></td></tr>';
    }).join('');
    return mihomoInbounds;
  } catch(e) {
    $('#mihomo-status').textContent = 'Mihomo: ' + e.message;
    $('#iempty').textContent = 'Mihomo 未接入或读取失败: ' + e.message;
    $('#iempty').style.display = '';
    return [];
  }
}

async function loadMihomoTemplates(){
  mihomoTemplates = await api('/api/mihomo/templates') || [];
  $('#mtemplate').innerHTML = mihomoTemplates.map(t => '<option value="' + esc(t.name) + '"' + (t.ready ? '' : ' disabled') + '>' + esc(t.name + ' · ' + t.mode + (t.ready ? '' : ' · ' + t.reason)) + '</option>').join('');
  fillMihomoTemplate();
}

function fillMihomoTemplate(){
  const t = mihomoTemplates.find(x => x.name === $('#mtemplate').value) || mihomoTemplates.find(x => x.ready);
  if(!t) return;
  $('#mtemplate').value = t.name;
  $('#mmode').value = t.mode || 'direct';
  $('#maddress').value = t.public_address || '';
  $('#mport').value = t.public_port || '';
  $('#mhost').value = t.host || '';
  $('#msni').value = t.sni || '';
  $('#mpath').value = t.path || '';
  $('#mpublickey').value = t.public_key || '';
  $('#mshortid').value = t.short_id || '';
}

const mihomoModal = $('#mihomomodal');
$('#mtemplate').onchange = fillMihomoTemplate;
$('#closemihomo').onclick = () => mihomoModal.classList.remove('open');
mihomoModal.onclick = e => { if(e.target === mihomoModal) mihomoModal.classList.remove('open'); };

$('#createMihomo').onclick = async () => {
  const up = tunnels.filter(t => t.status === 'up' && picked.has(t.slot));
  if(!up.length){ alert('先在左侧勾选已连接的出口'); return; }
  try { await loadMihomoTemplates(); }
  catch(err){ alert('读取 Mihomo 入站模板失败: ' + err.message); return; }
  if(!mihomoTemplates.some(t => t.ready)){ alert('没有可用的 Mihomo VLESS 入站模板'); return; }
  $('#mselected').textContent = up.length + ' 个出口';
  mihomoModal.classList.add('open');
};

$('#confirmMihomo').onclick = async e => {
  const up = tunnels.filter(t => t.status === 'up' && picked.has(t.slot));
  if(!up.length){ mihomoModal.classList.remove('open'); return; }
  const used = new Set(mihomoInbounds.map(i => i.name));
  const nextName = country => {
    const base = (country || 'Exit') + '-Fanout';
    let name = base, n = 2;
    while(used.has(name)) name = base + '-' + n++;
    used.add(name);
    return name;
  };
  e.target.disabled = true;
  e.target.textContent = '创建中';
  try {
    const created = [];
    for(const t of up){
      const name = nextName(t.node.country_code);
      const params = new URLSearchParams({name, port:String(t.port), template:$('#mtemplate').value, mode:$('#mmode').value, public_address:$('#maddress').value, public_port:$('#mport').value, host:$('#mhost').value, sni:$('#msni').value, path:$('#mpath').value, public_key:$('#mpublickey').value, short_id:$('#mshortid').value});
      await api('/api/mihomo/add?' + params.toString(), {method:'POST'});
      created.push(name);
    }
    const refreshed = await loadMihomo();
    const visible = new Set(refreshed.map(i => i.name));
    if(created.some(name => !visible.has(name))) throw new Error('创建命令已执行，但新入站未能从 Mihomo 绑定列表读回');
    mihomoModal.classList.remove('open');
    alert('已创建 ' + up.length + ' 个 Mihomo 入站');
  } catch(err) { alert('创建失败: ' + err.message); }
  e.target.disabled = false;
  e.target.textContent = '从选中出口创建';
};

document.addEventListener('change', async e => {
  const name = e.target.dataset.bindMihomo;
  if(!name) return;
  e.target.disabled = true;
  try { await api('/api/mihomo/bind?name=' + encodeURIComponent(name) + '&port=' + encodeURIComponent(e.target.value), {method:'POST'}); await loadMihomo(); }
  catch(err){ alert('切换出口失败: ' + err.message); await loadMihomo(); }
});

document.addEventListener('click', async e => {
  const name = e.target.dataset.deleteMihomo;
  if(!name) return;
  if(!confirm('删除 Mihomo 入站「' + name + '」？')) return;
  e.target.disabled = true;
  try { await api('/api/mihomo/delete?name=' + encodeURIComponent(name), {method:'POST'}); await loadMihomo(); }
  catch(err) { alert('删除失败: ' + err.message); e.target.disabled = false; }
});

$('#reloadin').onclick = loadMihomo;

const xmodal = $('#exportmodal');
$('#closeexport').onclick = () => xmodal.classList.remove('open');
xmodal.onclick = e => { if(e.target === xmodal) xmodal.classList.remove('open'); };

$('#exportBtn').onclick = async e => {
  if(!mihomoInbounds.length){ alert('还没有 Mihomo 入站'); return; }
  const links = mihomoInbounds.map(i => i.link).filter(Boolean);
  $('#exbox').value = links.join('\n');
  $('#excount').textContent = links.length + ' 条';
  xmodal.classList.add('open');
};

$('#copyall').onclick = async e => {
  const v = $('#exbox').value;
  if(!v) return;
  try{
    await navigator.clipboard.writeText(v);
    e.target.textContent = '已复制';
    setTimeout(() => { e.target.textContent = '全部复制'; }, 1200);
  }catch(err){
    $('#exbox').select();
    alert('自动复制失败，已选中，请按 Cmd/Ctrl+C');
  }
};

document.addEventListener('click', async e => {
  const val = e.target.dataset.copy;
  if(!val) return;
  try{
    await navigator.clipboard.writeText(val);
    const old = e.target.textContent;
    e.target.textContent = '已复制';
    setTimeout(() => { e.target.textContent = old; }, 1200);
  }catch(err){ alert('复制失败，请手动选中'); }
});

const modal = $('#nodemodal');
$('#addnode').onclick = () => { modal.classList.add('open'); $('#filter').focus(); };
$('#closemodal').onclick = () => modal.classList.remove('open');
modal.onclick = e => { if(e.target === modal) modal.classList.remove('open'); };
document.addEventListener('keydown', e => {
  if(e.key === 'Escape'){
    modal.classList.remove('open');
    xmodal.classList.remove('open');
  }
});

// 勾选出口：单个 + 全选
document.addEventListener('change', e => {
  if(e.target.classList.contains('pick')){
    const slot = Number(e.target.value);
    e.target.checked ? picked.add(slot) : picked.delete(slot);
    updatePickCount();
  }
  if(e.target.id === 'checkall'){
    picked.clear();
    if(e.target.checked){
      tunnels.filter(t => t.status === 'up').forEach(t => picked.add(t.slot));
    }
    renderTunnels(); updatePickCount();
  }
});

function updatePickCount(){
  const n = picked.size;
  $('#tcount').textContent = tunnels.length
    ? tunnels.length + ' 条' + (n ? '，已勾选 ' + n : '')
    : '';
}

loadNodes().catch(()=>{});
poll();
loadMihomo();
setInterval(poll, 3000);
</script>
</body>
</html>`
