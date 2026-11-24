const fhElement=(tag,text)=>{const node=document.createElement(tag);node.textContent=text;return node;};
const $=s=>document.querySelector(s);
let connectionInfo;
function error(message=''){$('#error').textContent=message;$('#error').hidden=!message;}
async function load(){
 const response=await fetch('/api/onboarding/info',{cache:'no-store'});const info=await response.json();
 if(response.status===401){$('#login').hidden=false;$('#content').hidden=true;return;}
 if(!response.ok)throw Error(fhServerText(info.error)||fhText('getSettingsFailed'));
 connectionInfo=info;
 $('#login').hidden=true;$('#content').hidden=false;error();$('#server').value=info.serverUrl;$('#username').textContent=info.username;
 $('#qr').src='/api/qr?'+new URLSearchParams({data:info.serverUrl+'/connect'});
 document.querySelectorAll('[data-photo-source]').forEach(node=>node.textContent=info.photoSource);
 $('#phone-folder').textContent=info.phoneFolder;
 $('#photos-scope-hint').hidden=!info.photoSource;
 $('#computer-photos').hidden=!info.photoSource;
 const usesHTTPS=new URL(info.serverUrl).protocol==='https:';
 $('#phone-photos').hidden=!info.photoSource||!usesHTTPS;
 $('#phone-manual-photos').hidden=!info.photoSource||usesHTTPS;
 $('#photos-later').hidden=info.photoDirs?.length!==0;
 $('#open-photos').hidden=!info.photoSource;
 if(info.photoSource)$('#open-photos').href='/#'+encodeURIComponent(info.photoSource)+'|'+encodeURIComponent(info.photoPath||'');
 renderStorage();
 if(new URLSearchParams(location.search).has('welcome'))fhAssignText($('#heading'),'readyHeading');
}
function renderStorage(){
 const sources=connectionInfo?.sources;
 if(!sources)return;
 $('#storage').hidden=false;
 const list=$('#locations');
 list.replaceChildren();
 for(const item of sources)list.append(fhElement('dt',item.name),fhElement('dd',item.storage+' / '+(item.path||fhText('storageRoot'))));
}
$('#login-form').onsubmit=async event=>{event.preventDefault();error();const state=await(await fetch('/api/onboarding/status',{cache:'no-store'})).json();const response=await fetch('/api/onboarding/login',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':state.csrf},body:JSON.stringify({username:$('#user').value,password:$('#password').value})});if(!response.ok){error(fhText('signInFailed'));return;}$('#password').value='';load().catch(e=>error(e.message));};
$('#copy').onclick=async()=>{try{await navigator.clipboard.writeText($('#server').value);$('#copy-status').textContent=fhText('addressCopied');}catch{$('#server').focus();$('#server').select();$('#copy-status').textContent=fhText('addressSelected');}};
window.addEventListener('fh-language-change',()=>{
 if(connectionInfo)document.querySelectorAll('[data-photo-source]').forEach(node=>node.textContent=connectionInfo.photoSource);
 renderStorage();
 fhRefreshNotice($('#error'));
 fhRefreshNotice($('#copy-status'));
});
load().catch(e=>error(e.message));
