/* Disk selection shared by setup and settings. */
window.StorageDevices=class {
 constructor(api,update,onStorages,{showMounted=true,storagesEndpoint='/api/onboarding/status'}={}){
  Object.assign(this,{api,update,onStorages,showMounted,storagesEndpoint,generation:0});
  document.querySelector('#refresh-devices').onclick=()=>this.refresh(true);
 }
 button(parent,text,action,kind='secondary'){const button=fhElement('button',text,kind);button.type='button';button.onclick=action;parent.append(button);return button;}
 proxmoxInstructions(){
  const guide=fhElement('div',undefined,'note'),steps=fhElement('ol');
  const run=fhElement('li',fhText('proxmoxRunInstaller'));
  run.append(fhElement('pre','bash ./install.sh --proxmox CTID --restart'),fhElement('div',fhText('proxmoxContainerNumber')));
  steps.append(fhElement('li',fhText('proxmoxOpenShell')),run,fhElement('li',fhText('proxmoxPickDrive')));
  guide.append(fhElement('strong',fhText('proxmoxConnectDrive')),steps,fhElement('small',fhText('mountedFolderProxmox')));
  return guide;
 }
 async refresh(reloadStorages=false){
  const generation=++this.generation;const target=document.querySelector('#devices'),status=document.querySelector('#device-status');target.replaceChildren();status.textContent=fhText('checkingHost');
  try{
   const data=await this.api('/api/onboarding/devices');if(generation!==this.generation)return;
   if(reloadStorages&&this.onStorages){
    const state=await this.api(this.storagesEndpoint);if(generation!==this.generation)return;
    this.onStorages(state.storages);
   }
   status.textContent='';
   if(!data.available){
    const host=data.connection?.environment;
    if(host==='lxc'&&!data.connection?.storageHostConfigured){
     target.append(this.proxmoxInstructions());return;
    }
    const hint=data.connection?.storageHostConfigured?'diskServiceUnavailable':host==='docker'?'mountedFolderContainer':'mountedFolderServer';
    const notice=fhElement('div',fhText(hint),'note');
    target.append(notice);return;
   }
   if(!data.volumes.length){target.append(fhElement('div',fhText('noDrive'),'note'));return;}
   for(const device of data.volumes){
    if(device.mounted&&!this.showMounted)continue;
    const row=fhElement('div',undefined,'device-row'),text=fhElement('div');
    text.append(fhElement('strong',device.label||device.device),fhElement('small',this.size(device.size)+(device.filesystem?' · '+device.filesystem:'')));
    text.append(fhElement('small',device.mounted?fhText('driveMounted'):fhServerText(device.reason)||fhText('driveReady')));
    if(device.testDevice)text.append(fhElement('small',fhText('testImage')));
    row.append(text);
    if(device.mounted||device.canMount){
     const button=this.button(row,device.mounted?fhText('useDrive'):fhText('mountDrive'),async()=>{
      button.disabled=true;status.textContent=device.mounted?fhText('selectingDrive'):fhText('mountingDrive');
      try{if(!device.mounted)await this.api('/api/onboarding/attach',{uuid:device.uuid});await this.update(device);await this.refresh(true);status.textContent=fhText('driveSelected');}
      catch(error){status.textContent=error.message;button.disabled=false;}
     },'');
    }
    target.append(row);
   }
  }catch(error){if(generation===this.generation)status.textContent=error.message;}
 }
 size(bytes){return fhCapacity(bytes,bytes<2**30 ? 'MiB' : 'GiB');}
};
