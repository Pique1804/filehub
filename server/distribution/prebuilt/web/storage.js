const $=selector=>document.querySelector(selector);
let csrf='',selection,revision='';
function error(message=''){$('#error').textContent=message;$('#error').hidden=!message;}
async function api(path,data){if(data){const state=await(await fetch('/api/onboarding/status',{cache:'no-store'})).json();csrf=state.csrf;}const response=await fetch(path,{cache:'no-store',...(data?{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify(data)}:{})});const result=await response.json();if(!response.ok){const failure=Error(fhServerText(result.error)||fhText('requestFailed'));failure.status=response.status;throw failure;}return result;}
async function load(){
 const state=await api('/api/onboarding/status');csrf=state.csrf;
 if(!state.configured){location.replace('/setup');return;}
 try{const data=await api('/api/onboarding/storage');revision=data.revision;selection=new StorageSelection($('#sources-picker'),$('#photos-picker'),data.storages,data.sources,data.photoDirs.map(p=>({...p,includeExisting:true})),true);$('#login').hidden=true;$('#content').hidden=false;$('#status').textContent=fhServerText(data.error)||(data.ready?fhText('serverRunning'):fhText('serverStopped'));await devices();}
 catch(e){if(e.status===401){$('#login').hidden=false;$('#content').hidden=true;return;}throw e;}
}
const devicePicker=new StorageDevices(api,async device=>{selection.storages=(await api('/api/onboarding/storage')).storages;const storage=selection.storages.find(s=>s.id==='uuid-'+device.uuid);if(!storage)throw Error(fhText('driveNotReady'));selection.addStorage(storage);},storages=>{
 selection.storages=storages;selection.render();
},{storagesEndpoint:'/api/onboarding/storage'});
async function devices(){await devicePicker.refresh();}
$('#login-form').onsubmit=async event=>{event.preventDefault();error();try{await api('/api/onboarding/login',{username:$('#user').value,password:$('#password').value});$('#password').value='';await load();}catch(e){error(e.message);}};
$('#settings').onsubmit=async event=>{event.preventDefault();error();$('#save').disabled=true;try{if(!await selection.checkPhotos(api)){error(fhText('confirmStoragePhotos'));return;}if(!confirm(fhText('confirmRestart')))return;const result=await api('/api/onboarding/storage',{...selection.data(),revision});await load();if(!result.ready)error(fhServerText(result.error)||fhText('savedServerFailed'));}catch(e){error(e.message);}finally{$('#save').disabled=false;}};
window.addEventListener('fh-language-change',()=>{
 selection?.render();
 if(selection)devices();
 fhRefreshNotice($('#error'));
 fhRefreshNotice($('#status'));
});
load().catch(e=>error(e.message));
