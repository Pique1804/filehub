/* Shared source/folder selection. Server-provided labels are text nodes. */
const fhElement=(tag,text,className)=>{const node=document.createElement(tag);if(text!==undefined)node.textContent=text;if(className)node.className=className;return node;};
window.StorageSelection=class {
 constructor(sourceTarget,photoTarget,storages,sources,photos,locked=false,allowAdd=true){Object.assign(this,{sourceTarget,photoTarget,storages,sources:structuredClone(sources),photos:structuredClone(photos),locked:locked?sources.length:0,allowAdd,photoReviews:[]});this.render();}
 field(parent,label,value,change,disabled=false){const input=fhElement('input');input.value=value;input.maxLength=512;input.disabled=disabled;input.setAttribute('aria-label',label);input.oninput=()=>{change(input.value);this.changed();};const wrapper=fhElement('label',label);wrapper.append(input);parent.append(wrapper);return input;}
 button(parent,label,action){const button=fhElement('button',label,'secondary');button.type='button';button.onclick=action;parent.append(button);return button;}
 changed(){
  this.sourceTarget.querySelectorAll('.storage-card > .visibility-line').forEach((node,index)=>{const source=this.sources[index],storage=this.storages.find(s=>s.id===source?.storage);if(source)node.textContent=fhText('inFiles')+': '+(storage?.name||source.storage)+' / '+(source.path||fhText('allContents'));});
  this.photoTarget.querySelectorAll('.storage-card > .visibility-line').forEach((node,index)=>{const photo=this.photos[index],source=this.sources.find(s=>s.name===photo?.source),storage=this.storages.find(s=>s.id===source?.storage);if(photo)node.textContent=fhText('inPhotos')+': '+(storage?.name||photo.source)+' / '+([source?.path,photo.path].filter(Boolean).join('/')||fhText('allContents'));});
  this.onChange?.();
 }
 render(){this.renderSources();this.renderPhotos();this.changed();}
 setPath(source,path,preserve=false){const before=source.path;if(before===path)return;for(const photo of this.photos.filter(p=>p.source===source.name)){if(preserve){const full=[before,photo.path].filter(Boolean).join('/');photo.path=!path?full:(full===path?'':full.startsWith(path+'/')?full.slice(path.length+1):'Photos');}photo.includeExisting=false;}source.path=path;this.photoReviews=[];}
 renderSources(){
  const target=this.sourceTarget;target.replaceChildren();
  this.sources.forEach((source,index)=>{
   const row=fhElement('div',undefined,'storage-card'),storage=this.storages.find(s=>s.id===source.storage);
   row.append(fhElement('strong',source.name+' · '+(storage?.name||source.storage)+(source.online===false||storage?.available===false?fhText('driveDisconnected'):'')));
   row.append(fhElement('small',source.path?fhText('folderPrefix')+source.path:(storage?.wholeDisk?fhText('diskRoot'):fhText('availableFolder'))));
   if(storage?.total)row.append(fhElement('small',fhText('freeOfTotal',{free:this.format(storage.free),total:this.format(storage.total)})));
   const locked=index<this.locked,whole=source.path==='';
   if(!this.diskFoldersInline){
    const modes=fhElement('fieldset',undefined,'source-scope');modes.append(fhElement('legend',fhText('scopeLabel')));
    for(const [value,text]of [['all',storage?.wholeDisk?fhText('wholeDisk'):fhText('availableFolder')],['folder',fhText('onlySelectedFolder')]]){const label=fhElement('label',undefined,'scope-choice'),radio=fhElement('input');radio.type='radio';radio.name='source-scope-'+index;radio.value=value;radio.checked=(value==='all')===whole;radio.disabled=locked;radio.onchange=()=>{this.setPath(source,value==='all'?'':storage?.defaultPath||'Filehub',true);this.render();};label.append(radio,document.createTextNode(text));modes.append(label);}row.append(modes);
    if(!whole){this.field(row,fhText('fileFolder'),source.path,value=>this.setPath(source,value),locked);if(!locked)this.button(row,fhText('browseFolder'),()=>this.browse(source.storage,'',source.path,value=>{this.setPath(source,value,true);this.render();}));}
   }
   if(whole&&storage?.wholeDisk&&!storage.rootWritable)row.append(fhElement('p',fhText('readOnlyRoot'),'note'));
   row.append(fhElement('p',fhText('inFiles')+': '+(storage?.name||source.storage)+(source.path?' / '+source.path:' / '+fhText('allContents')),'visibility-line'));
   const details=fhElement('details');details.append(fhElement('summary',fhText('displayName')));
   const name=this.field(details,fhText('sourceName'),source.name,value=>{const old=source.name;source.name=value;this.photos.forEach(p=>{if(p.source===old)p.source=value;});this.renderPhotos();},locked);
   name.maxLength=32;details.append(fhElement('small',fhText('sourceNameHint')));
   if(!locked&&this.allowAdd)this.button(details,fhText('removeFromList'),()=>{this.sources.splice(index,1);this.render();});
   row.append(details);target.append(row);
  });
  if(!this.allowAdd)return;
  const controls=fhElement('div',undefined,'source-add');
  const choices=fhElement('select');choices.setAttribute('aria-label',fhText('newFolderStorage'));
  this.storages.forEach(storage=>{const option=fhElement('option',storage.name+(storage.available?'':fhText('driveUnavailable')));option.value=storage.id;option.disabled=!storage.available;choices.append(option);});controls.append(choices);
  this.button(controls,fhText('addDiskOrFolder'),()=>{if(this.sources.length>=16)return this.fail(fhText('storageLimit'));const storage=this.storages.find(s=>s.id===choices.value&&s.available);if(!storage)return this.fail(fhText('noFolderAvailable'));this.addStorage(storage);});target.append(controls);
 }
 nextSourceName(preferred=''){
  const names=new Set(this.sources.map(source=>source.name.toLowerCase()));
  if(/^[A-Za-z0-9_-]{1,32}$/.test(preferred)&&!names.has(preferred.toLowerCase()))return preferred;
  let number=this.sources.length+1;
  while(names.has('disk'+number))number++;
  return 'Disk'+number;
 }
 addStorage(storage){if(this.sources.some(s=>s.storage===storage.id&&s.path===(storage.defaultPath||'')))return;this.sources.push({name:this.nextSourceName(),storage:storage.id,path:storage.defaultPath||''});this.render();}
 renderPhotos(){
  const target=this.photoTarget;target.replaceChildren();
  this.photos.forEach((photo,index)=>{
   const row=fhElement('div',undefined,'storage-card'),label=fhElement('label',fhText('photoStorage')),select=fhElement('select');select.setAttribute('aria-label',fhText('photoStorage'));
   this.sources.forEach(source=>{const option=fhElement('option',source.name);option.value=source.name;select.append(option);});select.value=photo.source;
   const review=this.photoReviews.find(p=>p.source===photo.source&&p.path===photo.path);const confirmation=fhElement('input');confirmation.type='checkbox';confirmation.setAttribute('aria-label',fhText('includeExisting'));confirmation.checked=!!photo.includeExisting;confirmation.onchange=()=>{photo.includeExisting=confirmation.checked;this.changed();};
   select.onchange=()=>{photo.source=select.value;photo.includeExisting=false;confirmation.checked=false;this.changed();};label.append(select);row.append(label);
   this.field(row,fhText('photosFolder'),photo.path,value=>{photo.path=value;photo.includeExisting=false;confirmation.checked=false;});
   const source=this.sources.find(s=>s.name===photo.source),storage=this.storages.find(s=>s.id===source?.storage);row.append(fhElement('p',fhText('inPhotos')+': '+(storage?.name||photo.source)+' / '+([source?.path,photo.path].filter(Boolean).join('/')||fhText('allContents')),'visibility-line'));
   this.button(row,fhText('pickPhotosFolder'),()=>{const source=this.sources.find(s=>s.name===photo.source);if(!source)return this.fail(fhText('selectSource'));this.browse(source.storage,source.path,photo.path,value=>{photo.path=value;photo.includeExisting=true;this.renderPhotos();});});
   if(review?.needsConfirmation&&!photo.includeExisting){const warning=fhElement('p',fhText('confirmPhotosHint'),'note photo-warning');row.append(warning);confirmation.setAttribute('aria-describedby','photo-warning-'+index);warning.id='photo-warning-'+index;row.classList.add('needs-confirmation');}const check=fhElement('label',undefined,'check');check.append(confirmation,document.createTextNode(fhText('includeExistingHint')));row.append(check);
   this.button(row,fhText('removeFromPhotos'),()=>{this.photos.splice(index,1);this.renderPhotos();});target.append(row);
  });
  this.changed();this.button(target,fhText('addPhotoFolder'),()=>{if(!this.sources.length)return this.fail(fhText('selectStorageFirst'));if(this.photos.length>=32)return this.fail(fhText('photoFolderLimit'));this.photos.push({source:this.sources[0].name,path:'Photos',includeExisting:false});this.renderPhotos();});
 }
 async checkPhotos(api,focus=true){const request=this.data(),key=JSON.stringify(request);const report=await api('/api/onboarding/validate',request);if(JSON.stringify(this.data())!==key)return false;this.photoReviews=report.photoDirs;this.renderPhotos();if(!report.valid&&focus){const check=this.photoTarget.querySelector('.needs-confirmation input[type=checkbox]:not(:checked)');check?.focus();}return report.valid;}
 folderPicker(storage,base,current,accept){
  const panel=fhElement('div',undefined,'folder-picker'),location=fhElement('p',undefined,'path'),tree=fhElement('ul',undefined,'folder-tree'),preview=fhElement('div',undefined,'folder-preview'),status=fhElement('p');let selected='',revision=0;const nodes=new Map();
  status.setAttribute('role','status');
  const viewport=fhElement('div',undefined,'folder-tree-viewport');
  viewport.setAttribute('role','region');
  viewport.setAttribute('aria-label',fhText('folderTreeLabel'));
  viewport.append(tree);
  const scrollHint=fhElement('p',undefined,'folder-scroll-hint');
  scrollHint.hidden=true;
  const updateScrollHint=()=>{
   const overflow=viewport.scrollHeight-viewport.clientHeight;
   scrollHint.hidden=overflow<=1;
   viewport.tabIndex=scrollHint.hidden?-1:0;
   if(!scrollHint.hidden){
    const label=fhText(viewport.scrollTop<overflow-1?'foldersBelow':'foldersAbove');
    if(scrollHint.textContent!==label)scrollHint.textContent=label;
   }
  };
  const size=new ResizeObserver(updateScrollHint);
  size.observe(viewport);size.observe(tree);
  viewport.addEventListener('scroll',updateScrollHint,{passive:true});
  const actions=fhElement('div',undefined,'folder-picker-actions');
  const choose=this.button(actions,fhText('pickThisFolder'),()=>accept(selected),'');
  panel.append(location,viewport,scrollHint,preview,status,actions);
  const read=async path=>{const response=await fetch('/api/onboarding/folders?'+new URLSearchParams({storage,path:[base,path].filter(Boolean).join('/')}),{cache:'no-store'}),data=await response.json();if(!response.ok)throw Error(fhServerText(data.error)||fhText('folderFailed'));return data;};
  const select=async(path,node)=>{const request=++revision;selected=path;choose.disabled=true;location.textContent=[base,path].filter(Boolean).join('/')||fhText('allContents');nodes.forEach(n=>n.summary.classList.toggle('selected',n===node));preview.replaceChildren();status.textContent=fhText('openFolderProgress');try{const data=await load(node,path);if(request!==revision||!panel.isConnected)return;preview.append(fhElement('strong',fhText('selectedFolderContents')));const list=fhElement('ul');for(const item of data.samples||[])list.append(fhElement('li',item.name+(item.kind==='folder'?' /':'')));preview.append(list,fhElement('small',fhText('folderCounts',{folders:fhNumber(data.folders.length),media:fhNumber(data.mediaHere)})));status.textContent=data.truncated?fhText('folderListLimit'):(data.hasEntries?fhText('folderExpand'):fhText('emptyFolder'));choose.disabled=false;}catch(error){if(request===revision)status.textContent=error.message;}};
  const add=(parent,label,path)=>{const li=fhElement('li'),details=fhElement('details'),summary=fhElement('summary',label),children=fhElement('ul');details.append(summary,children);li.append(details);parent.append(li);const node={details,summary,children,path};nodes.set(path,node);summary.onclick=()=>select(path,node);details.ontoggle=()=>{if(details.open)load(node,path).catch(error=>{status.textContent=error.message;});};return node;};
  const load=(node,path)=>{if(!node.loading)node.loading=read(path).then(data=>{for(const name of data.folders)add(node.children,name,[path,name].filter(Boolean).join('/'));return data;}).catch(error=>{node.loading=null;throw error;});return node.loading;};
  const root=add(tree,fhText('allFolders'),'');
  const open=async()=>{
   root.details.open=true;
   const initialRevision=revision+1;
   await select('',root);
   if(!current||revision!==initialRevision||choose.disabled||!panel.isConnected)return;
   let path='',node=root;
   try{
    for(const part of current.split('/')){
     await load(node,path);
     if(!panel.isConnected||revision!==initialRevision)return;
     path=[path,part].filter(Boolean).join('/');const child=nodes.get(path);if(!child)break;
     child.details.open=true;node=child;
    }
    if(node!==root)await select(node.path,node);
   }catch(error){if(revision===initialRevision)status.textContent=error.message;}
  };
  choose.disabled=true;
  return {panel,open,actions,dispose:()=>{size.disconnect();panel.remove();}};
 }
 async browse(storage,base,current,accept){
  const dialog=fhElement('dialog'),heading=fhElement('h2',fhText('pickFolderHeading'));
  const close=()=>dialog.close();
  const picker=this.folderPicker(storage,base,current,value=>{accept(value);close();});
  const cancel=this.button(picker.actions,fhText('cancel'),close);
  picker.actions.prepend(cancel);
  dialog.append(heading,picker.panel);
  dialog.addEventListener('close',()=>{picker.dispose();dialog.remove();},{once:true});
  document.body.append(dialog);dialog.showModal();cancel.focus();await picker.open();
 }
 fail(message){document.querySelector('#error').textContent=message;document.querySelector('#error').hidden=false;}
 format(bytes){return fhCapacity(bytes);}
 data(){if(!this.sources.length)throw Error(fhText('selectLocation'));if(this.sources.some(s=>!s.name.match(/^[A-Za-z0-9_-]{1,32}$/)))throw Error(fhText('sourceNameInvalid'));return{sources:this.sources.map(({name,storage,path})=>({name,storage,path})),photoDirs:structuredClone(this.photos)};}
};
