const $=selector=>document.querySelector(selector);
const enrollment=new URLSearchParams(location.hash.slice(1)).get('code')||'';
if(enrollment){$('#setup-code').value=enrollment;history.replaceState(null,'',location.pathname+location.search);}
let step=0,csrf='',selection,devicePicker,serverStorage;
let diskMode=false,diskChosen=false,authenticated=false,restoring=true,finishing=false;
let timer,saveQueue=Promise.resolve(),lastSaved='',lastQueued='';
const diskRows=new Map();
function error(message=''){$('#error').textContent=message;$('#error').hidden=!message;}
async function api(path,data){
 if(data){const state=await(await fetch('/api/onboarding/status',{cache:'no-store'})).json();csrf=state.csrf;}
 const response=await fetch(path,{cache:'no-store',...(data?{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify(data)}:{})});
 const result=await response.json();if(!response.ok){const failure=Error(fhServerText(result.error)||fhText('requestFailed'));failure.step=result.step;failure.status=response.status;throw failure;}return result;
}
function progress(){return{step:Math.max(1,step),mode:diskMode?'disk':'server',diskChosen,sources:selection.sources.map(({name,storage,path})=>({name,storage,path})),photoDirs:selection.photos.map(({source,path,includeExisting})=>({source,path,includeExisting:!!includeExisting}))};}
function saveProgress(){
 clearTimeout(timer);if(restoring||!authenticated||finishing||step===0||!selection)return Promise.resolve();
 const value=progress(),key=JSON.stringify(value);if(key===lastQueued){if(key===lastSaved)$('#progress-status').textContent=fhText('stepSaved');return saveQueue;}lastQueued=key;
 $('#progress-status').textContent=fhText('savingStep');
 saveQueue=saveQueue.catch(()=>{}).then(async()=>{
  if(!authenticated||finishing)return;
  try{await api('/api/onboarding/draft',value);lastSaved=key;if(key===lastQueued)$('#progress-status').textContent=fhText('stepSaved');}
  catch(e){if(lastQueued===key)lastQueued='';$('#progress-status').textContent=fhText('stepNotSaved');if(e.status===401)loginRequired();else error(e.message);}
 });return saveQueue;
}
function changed(){updateVisibility();if(restoring||!authenticated||finishing||step===0)return;$('#progress-status').textContent=JSON.stringify(progress())===lastSaved&&lastQueued===lastSaved?fhText('stepSaved'):fhText('savingStep');clearTimeout(timer);timer=setTimeout(saveProgress,350);}
function updateStepCounter(){$('#step-counter').textContent=fhText('stepCounter',{current:fhNumber(step+1)});}
function show(value){step=value;document.querySelectorAll('[data-step]').forEach(e=>e.hidden=Number(e.dataset.step)!==value);document.querySelectorAll('.steps li').forEach((e,i)=>{if(i===value)e.setAttribute('aria-current','step');else e.removeAttribute('aria-current');});updateStepCounter();error();window.scrollTo(0,0);changed();}
function storageLabel(source){const storage=selection.storages.find(s=>s.id===source.storage);return(storage?.name||source.storage)+' / '+(source.path||fhText('allContents'));}
function updateVisibility(){
 if(!selection)return;const target=$('#visibility-preview');target.replaceChildren();
 for(const source of selection.sources)target.append(fhElement('dt',fhText('inFiles')),fhElement('dd',source.name+' — '+storageLabel(source)));
 for(const photo of selection.photos){const source=selection.sources.find(s=>s.name===photo.source);if(source){const storage=selection.storages.find(s=>s.id===source.storage);target.append(fhElement('dt',fhText('inPhotos')),fhElement('dd',(storage?.name||source.storage)+' / '+([source.path,photo.path].filter(Boolean).join('/')||fhText('allContents'))));}}
}
function makeDiskRow(id){
 const element=fhElement('div',undefined,'setup-disk'),line=fhElement('div',undefined,'setup-disk-line');
 const label=fhElement('label',undefined,'check'),check=fhElement('input'),name=fhElement('strong');
 check.type='checkbox';label.append(check,name);
 const caption=fhElement('small'),details=fhElement('details'),summary=fhElement('summary',fhText('browseFolder'));
 details.append(summary);line.append(label,caption);element.append(line,details);
 const row={element,check,name,caption,details,summary};
 check.onchange=()=>{
  error();
  try{
   if(check.checked)void chooseDiskStorage(row.storage);
   else removeDiskSource(diskSource(id));
  }catch(e){check.checked=!!diskSource(id);failure(e);}
 };
 details.ontoggle=()=>{
  if(!details.open){row.picker?.dispose();row.picker=null;return;}
  if(row.picker)return;
  const current=diskSource(id)?.path??row.storage.defaultPath??'';
  const picker=selection.folderPicker(id,'',current,value=>{
   try{
    error();void chooseDiskStorage(row.storage,value);details.open=false;summary.focus();
   }catch(e){failure(e);}
  });
  row.picker=picker;details.append(picker.panel);void picker.open();
 };
 return row;
}
function renderMountedFolders(){
 const available=new Map();
 if(diskMode)for(const storage of selection.storages){
  if(storage.id!==serverStorage.id&&storage.available)available.set(storage.id,storage);
 }
 if(diskMode&&diskChosen)for(const source of selection.sources){
  if(!available.has(source.storage))available.set(source.storage,{id:source.storage,name:source.name,available:false});
 }
 const target=$('#mounted-folder-list');
 for(const [id,row] of diskRows)if(!available.has(id)){row.picker?.dispose();row.element.remove();diskRows.delete(id);}
 for(const storage of available.values()){
  let row=diskRows.get(storage.id);
  if(!row){row=makeDiskRow(storage.id);diskRows.set(storage.id,row);target.append(row.element);}
  row.storage=storage;
  const source=diskSource(storage.id);
  row.check.checked=!!source;
  row.name.textContent=storage.name;
  row.caption.textContent=storage.available?fhText('freeSpace',{size:fhCapacity(storage.free)}):fhText('driveUnavailable').trim().replace(/^·\s*/, '');
  row.details.hidden=!storage.available;
  if(!storage.available)row.details.open=false;
  row.summary.textContent=source?(source.path||fhText(storage.wholeDisk?'wholeDisk':'availableFolder'))+' · '+fhText('browseFolder'):fhText('browseFolder');
 }
 $('#mounted-folders').hidden=!available.size;
}
function diskSource(id){return diskMode&&diskChosen?selection.sources.find(source=>source.storage===id):undefined;}
function chooseDiskStorage(storage,path){
 const current=diskSource(storage.id);
 if(current){
  if(path===undefined||path===current.path)return;
  selection.setPath(current,path,true);
  updateMode();selection.render();return saveProgress();
 }
 if(diskChosen&&selection.sources.length>=16)throw Error(fhText('storageLimit'));
 if(!diskChosen){
  selection.sources=[];
  selection.photos=[];
 }
 const source={name:selection.nextSourceName(storage.name),storage:storage.id,path:path??storage.defaultPath??''};
 selection.sources.push(source);
 if(!diskChosen)selection.photos.push({source:source.name,path:'Photos',includeExisting:false});
 diskChosen=true;selection.photoReviews=[];updateMode();selection.render();return saveProgress();
}
function removeDiskSource(source){
 selection.sources=selection.sources.filter(item=>item!==source);
 selection.photos=selection.photos.filter(photo=>photo.source!==source.name);
 selection.photoReviews=[];
 diskChosen=selection.sources.length>0;
 updateMode();selection.render();void saveProgress();
}
function updateMode(){
 selection.diskFoldersInline=diskMode;
 $('#use-server').setAttribute('aria-pressed',String(!diskMode));$('#use-disk').setAttribute('aria-pressed',String(diskMode));$('#disk-panel').hidden=!diskMode;
 $('#selected-storage').hidden=diskMode&&!diskChosen;
 const offline=selection.sources.some(s=>!selection.storages.find(v=>v.id===s.storage)?.available);
 $('#storage-next').disabled=diskMode&&(!diskChosen||offline);
 renderMountedFolders();
}
function storageMode(disk){
 if(diskMode===disk)return;diskMode=disk;
 if(!disk){selection.sources=[{name:'Main',storage:serverStorage.id,path:serverStorage.defaultPath||''}];selection.photos=[{source:'Main',path:'Photos',includeExisting:false}];diskChosen=false;selection.photoReviews=[];}
 updateMode();selection.render();if(disk)devicePicker.refresh();
}
$('#use-server').onclick=()=>storageMode(false);$('#use-disk').onclick=()=>storageMode(true);
function accountForm(claimed){
 $('#setup-code-row').hidden=claimed;$('#setup-code').required=!claimed;$('#setup-code').disabled=claimed;
 fhAssignText($('#account-title'),claimed?'continueSetup':'createAccount');fhAssignText($('#account-hint'),claimed?'continueHint':'accountHint');
 $('#repeat-row').hidden=claimed;$('#repeat').disabled=claimed;$('#repeat').required=!claimed;$('#password-hint').hidden=claimed;$('#password').disabled=false;$('#password').required=true;$('#password').autocomplete=claimed?'current-password':'new-password';
}
function sessionReady(){authenticated=true;$('#setup-code').value='';$('#setup-code').required=false;$('#setup-code').disabled=true;$('#password').value='';$('#repeat').value='';$('#password').required=false;$('#password').disabled=true;$('#repeat').disabled=true;$('#repeat').required=false;}
function loginRequired(){authenticated=false;clearTimeout(timer);accountForm(true);show(0);error(fhText('signInToContinue'));}
function failure(e){if(e.status===401)loginRequired();else error(e.message);}
async function restoreProgress(){
 restoring=true;const data=await api('/api/onboarding/draft');const draft=data.draft;
 if(draft){selection.sources=structuredClone(draft.sources);selection.photos=structuredClone(draft.photoDirs);diskMode=draft.mode==='disk';diskChosen=draft.diskChosen;lastSaved=lastQueued=JSON.stringify(draft);}
 updateMode();selection.render();const offline=selection.sources.some(s=>!selection.storages.find(v=>v.id===s.storage)?.available);let target=draft?.step||1;if(offline)target=1;
 if(!selection.sources.length||diskMode&&!diskChosen)target=1;
 show(target);restoring=false;changed();if(draft)$('#progress-status').textContent=fhText('stepRestored');
 if(offline)error(fhText('selectedDriveOffline'));
 if(diskMode)await devicePicker.refresh();
 if(step>=2){const valid=await selection.checkPhotos(api,false);if(step===3&&!valid){show(2);error(fhText('confirmExistingPhotos'));}else if(step===3)summary();}
}
function summary(){
 const target=$('#summary');target.replaceChildren();
 const rows=[[fhText('account'),$('#username').value],...selection.sources.map(s=>[fhText('files'),s.name+' — '+storageLabel(s)])];
 for(const photo of selection.photos){
  const source=selection.sources.find(s=>s.name===photo.source),storage=selection.storages.find(s=>s.id===source?.storage);
  rows.push([fhText('photos'),(storage?.name||photo.source)+' / '+([source?.path,photo.path].filter(Boolean).join('/')||fhText('allContents'))]);
 }
 if(!selection.photos.length)rows.push([fhText('photos'),fhText('photosNotConfigured')]);
 for(const [label,value] of rows)target.append(fhElement('dt',label),fhElement('dd',value));
}
document.querySelectorAll('[data-next]').forEach(button=>button.onclick=async()=>{
 const from=step;
 const controls=from===2?document.querySelectorAll('[data-step="2"] [data-next]'):[button];
 for(const control of controls)control.disabled=true;
 try{
  if(step===0){
   if(!$('#username').reportValidity()||!$('#password').reportValidity())return;
   if(new TextEncoder().encode($('#password').value).length>72)throw Error(fhText('passwordMax'));
   if(!$('#repeat').disabled&&$('#password').value!==$('#repeat').value)throw Error(fhText('passwordMismatch'));
   await api('/api/onboarding/claim',{username:$('#username').value,password:$('#password').value,setupCode:$('#setup-code').value});sessionReady();await restoreProgress();return;
  }
  if(step===1&&diskMode&&!diskChosen)throw Error(fhText('selectDrive'));
  if(!selection.sources.length)throw Error(fhText('selectLocation'));
  if(from===2){
   if(button.hasAttribute('data-skip-photos')){
    selection.photos=[];selection.photoReviews=[];selection.renderPhotos();
   }else{
    const valid=await selection.checkPhotos(api);
    if(step!==from)return;
    if(!valid){error(fhText('chooseExistingPhotos'));return;}
   }
   summary();
  }
  if(step!==from)return;show(from+1);await saveProgress();
  if(step===2)try{await selection.checkPhotos(api,false);}catch(e){if(step===2)failure(e);}
 }catch(e){failure(e);}finally{for(const control of controls)control.disabled=false;updateMode();}
});
document.querySelectorAll('[data-back]').forEach(button=>button.onclick=()=>{show(Math.max(1,step-1));saveProgress();});
$('#wizard').onsubmit=async event=>{
 event.preventDefault();if(step!==3)return;error();$('#finish').disabled=true;
 try{
  if(!await selection.checkPhotos(api)){show(2);error(fhText('confirmExistingFolder'));return;}
  await saveProgress();finishing=true;clearTimeout(timer);
  const result=await api('/api/onboarding/setup',{username:$('#username').value,...selection.data()});
  if(!result.ready)throw Error(fhServerText(result.error)||fhText('savedOpenStorage'));location.assign('/connect?welcome=1');
 }catch(e){finishing=false;if(e.step===2){show(2);try{await selection.checkPhotos(api);}catch{}}failure(e);}finally{$('#finish').disabled=false;}
};
window.addEventListener('pagehide',()=>{if(authenticated&&!finishing&&step>0&&selection&&csrf)fetch('/api/onboarding/draft',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify(progress()),keepalive:true}).catch(()=>{});});
window.addEventListener('fh-language-change',()=>{
 updateStepCounter();
 if(serverStorage)$('#server-space').textContent=fhText('freeSpace',{size:fhCapacity(serverStorage.free)});
 if(selection){
  const onChange=selection.onChange;
  selection.onChange=undefined;
  try{selection.render();}finally{selection.onChange=onChange;}
  updateVisibility();
  if(step===3)summary();
 }
 if(diskMode){
  for(const row of diskRows.values()){row.picker?.dispose();row.element.remove();}
  diskRows.clear();renderMountedFolders();devicePicker?.refresh();
 }
 fhRefreshNotice($('#error'));
 fhRefreshNotice($('#progress-status'));
});
(async()=>{
 try{
  const data=await api('/api/onboarding/status');csrf=data.csrf;if(data.configured){location.replace(data.ready?'/connect':'/storage');return;}
  accountForm(data.claimed);serverStorage=data.storages.find(s=>s.id==='server'&&s.available)||data.storages.find(s=>s.available);
  if(!serverStorage)throw Error(fhText('serverFolderOffline'));
  $('#server-space').textContent=fhText('freeSpace',{size:fhCapacity(serverStorage.free)});
  selection=new StorageSelection($('#sources-picker'),$('#photos-picker'),data.storages,[{name:'Main',storage:serverStorage.id,path:serverStorage.defaultPath||''}],[{source:'Main',path:'Photos',includeExisting:false}],false,false);selection.onChange=changed;
  devicePicker=new StorageDevices(api,async device=>{
   if(!diskMode)return false;const storages=(await api('/api/onboarding/status')).storages;if(!diskMode)return false;selection.storages=storages;
   const storage=storages.find(s=>s.id==='uuid-'+device.uuid);if(!storage)throw Error(fhText('driveNotInContainer'));
   await chooseDiskStorage(storage);
  },storages=>{
   selection.storages=storages;updateMode();selection.render();
  },{showMounted:false});
  $('#loading').hidden=true;$('#wizard').hidden=false;
  if(data.authenticated){$('#username').value=data.username;sessionReady();await restoreProgress();}else{restoring=false;show(0);}
 }catch(e){restoring=false;$('#loading').hidden=true;failure(e);}
})();
