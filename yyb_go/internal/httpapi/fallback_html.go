package httpapi

const fallbackIndexHTML = `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>YYB Go 控制台</title>
<body style="margin:0;background:oklch(0.974 0.004 250);color:oklch(0.19 0.025 252);font-family:system-ui,-apple-system,BlinkMacSystemFont,'Segoe UI','Microsoft YaHei',sans-serif">
<main style="max-width:960px;margin:48px auto;padding:0 24px">
<section style="background:oklch(1 0 0);border:1px solid oklch(0.885 0.012 250);border-radius:8px;padding:24px">
<h1 style="margin:0 0 8px;font-size:24px">YYB Go 控制台</h1>
<p style="margin:0 0 20px;color:oklch(0.43 0.025 252)">资源模板未找到，服务仍可通过以下入口使用。</p>
<p style="display:flex;gap:10px;flex-wrap:wrap;margin:0">
<a style="padding:10px 12px;border-radius:8px;background:oklch(0.54 0.205 3);color:oklch(1 0 0);text-decoration:none" href="/scan">扫码添加</a>
<a style="padding:10px 12px;border-radius:8px;border:1px solid oklch(0.885 0.012 250);color:inherit;text-decoration:none" href="/docs/index.html">Swagger 文档</a>
<a style="padding:10px 12px;border-radius:8px;border:1px solid oklch(0.885 0.012 250);color:inherit;text-decoration:none" href="/openapi.json">OpenAPI JSON</a>
</p>
</section>
</main>
</body></html>`

const fallbackScanHTML = `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>扫码添加账号</title>
<body style="margin:0;min-height:100vh;display:grid;place-items:center;background:oklch(0.974 0.004 250);color:oklch(0.19 0.025 252);font-family:system-ui,-apple-system,BlinkMacSystemFont,'Segoe UI','Microsoft YaHei',sans-serif">
<main style="width:min(420px,calc(100vw - 32px));background:oklch(1 0 0);border:1px solid oklch(0.885 0.012 250);border-radius:8px;padding:24px;text-align:center">
<h1 style="margin:0 0 8px;font-size:22px">扫码添加账号</h1>
<p id="s" style="margin:0 0 18px;color:oklch(0.43 0.025 252)">正在生成二维码</p>
<div id="qr" style="width:240px;height:240px;margin:0 auto 18px;display:grid;place-items:center;border:1px solid oklch(0.885 0.012 250);border-radius:8px;background:oklch(0.986 0.004 250)">请稍候</div>
<p style="display:flex;gap:10px;justify-content:center;margin:0">
<button onclick="newQR()" style="border:0;border-radius:8px;padding:10px 12px;background:oklch(0.54 0.205 3);color:oklch(1 0 0)">重新生成</button>
<a href="/" style="border:1px solid oklch(0.885 0.012 250);border-radius:8px;padding:9px 12px;color:inherit;text-decoration:none">返回首页</a>
</p>
</main>
<script>
let sid,timer;
async function api(url,options){
 const resp=await fetch(url,options);
 const text=await resp.text();
 let data=null;
 try{data=text?JSON.parse(text):null}catch(e){data=text}
 const isEnvelope=data&&typeof data==='object'&&!Array.isArray(data)&&Object.prototype.hasOwnProperty.call(data,'code')&&Object.prototype.hasOwnProperty.call(data,'msg')&&Object.prototype.hasOwnProperty.call(data,'data');
 if(!resp.ok||(isEnvelope&&data.code!==0)){throw new Error(isEnvelope?data.msg:'HTTP '+resp.status)}
 return isEnvelope?data.data:data;
}
async function newQR(){
 clearInterval(timer);
 document.getElementById('qr').textContent='请稍候';
 document.getElementById('s').textContent='正在生成二维码';
 const r=await api('/qr',{method:'POST'});
 sid=r.session_id;
 document.getElementById('qr').innerHTML='<img alt="二维码" style="width:240px;height:240px" src="'+r.image_url+'">';
 document.getElementById('s').textContent='等待扫码';
 timer=setInterval(poll,1500);
}
async function poll(){
 const r=await api('/qr/'+sid+'/poll');
 document.getElementById('s').textContent=r.status;
 if(r.status==='authorized'){
  clearInterval(timer);
  const a=await api('/qr/'+sid+'/confirm',{method:'POST'});
  document.getElementById('s').textContent='添加成功: '+(a.nickname||a.openid);
 }
 if(['expired','cancelled','unknown'].includes(r.status)){clearInterval(timer)}
}
newQR();
</script></body></html>`

const fallbackLoginHTML = `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>登录 · YYB Go</title>
<body style="margin:0;min-height:100vh;display:grid;place-items:center;background:oklch(0.974 0.004 250);color:oklch(0.19 0.025 252);font-family:system-ui,-apple-system,BlinkMacSystemFont,'Segoe UI','Microsoft YaHei',sans-serif">
<main style="width:min(380px,calc(100vw - 32px));background:oklch(1 0 0);border:1px solid oklch(0.885 0.012 250);border-radius:8px;padding:28px">
<h1 style="margin:0 0 6px;font-size:22px">YYB Go 登录</h1>
<p style="margin:0 0 18px;color:oklch(0.43 0.025 252);font-size:13px">请输入管理员账号密码</p>
<form onsubmit="return doLogin(event)">
<label style="display:block;margin-bottom:12px;font-size:13px">用户名
<input id="u" name="username" autocomplete="username" required style="width:100%;margin-top:4px;padding:10px;border:1px solid oklch(0.885 0.012 250);border-radius:8px;box-sizing:border-box">
</label>
<label style="display:block;margin-bottom:16px;font-size:13px">密码
<input id="p" name="password" type="password" autocomplete="current-password" required style="width:100%;margin-top:4px;padding:10px;border:1px solid oklch(0.885 0.012 250);border-radius:8px;box-sizing:border-box">
</label>
<p id="err" style="display:none;margin:0 0 12px;color:oklch(0.55 0.18 25);font-size:13px"></p>
<button type="submit" style="width:100%;border:0;border-radius:8px;padding:11px;background:oklch(0.54 0.205 3);color:oklch(1 0 0);font-size:14px;cursor:pointer">登录</button>
</form>
<script>
async function doLogin(e){
 e.preventDefault();
 const body={username:document.getElementById('u').value,password:document.getElementById('p').value};
 const r=await fetch('/auth/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
 if(r.ok){location.href='/';return false}
 let msg='登录失败';
 try{const d=await r.json();if(d&&d.msg)msg=d.msg}catch(e2){}
 const err=document.getElementById('err');
 err.textContent=msg;err.style.display='block';
 return false;
}
</script></main></body></html>`

var openAPISpec = newOpenAPISpec()
