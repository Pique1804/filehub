import Uppy from '@uppy/core';
import Tus from '@uppy/tus';
import { uploadFingerprint } from './media-utils.js';
import { checkExistingUpload } from './upload-preflight.js';
import { uploadFileReader, uploadParentPath } from './file-operations.js';

export function createUploadManager(account, onchange, oncomplete, onResumeUnavailable = () => {}) {
  let alive = true;
  const preflights = new Map();
  const uppy = new Uppy({autoProceed:true, allowMultipleUploadBatches:true,
    onBeforeFileAdded:(file,files) => files[file.meta.uploadKey] ? false : {...file,id:file.meta.uploadKey}})
    .use(Tus, {endpoint:'/files/', limit:3, chunkSize:8*1024*1024, withCredentials:true,
      fileReader:uploadFileReader, retryDelays:[0,1000,3000,5000,10000], removeFingerprintOnSuccess:true,
      onShouldRetry(error, attempt, options, defaultRetry) {
        if (error.message.includes('ERR_FILEHUB_LOCAL_READ')) return false;
        if (error.originalResponse?.getStatus() === 507) return false;
        if (error.originalResponse?.getBody()?.includes('ERR_UNEXPECTED_EOF')) return attempt === 0;
        return defaultRetry(error, attempt, options);
      },
      onAfterResponse(request, response) {
        if (alive && request.getMethod() === 'HEAD' && [404,410].includes(response.getStatus())) {
          onResumeUnavailable();
        }
      },
      allowedMetaFields:['filename','source','path']});
  uppy.addPreProcessor(async ids => {
    let next = 0;
    async function checkFiles() {
      while (alive && next < ids.length) {
        const id = ids[next++];
        const file = uppy.getFile(id);
        if (!file || file.progress.uploadComplete) continue;
        const controller = new AbortController();
        const timeout = setTimeout(() => controller.abort(), 5 * 60 * 1000);
        preflights.set(id, controller);
        try {
          const receipt = await checkExistingUpload(file.data, file.meta.source, file.meta.path, controller.signal);
          if (alive && !controller.signal.aborted && receipt && uppy.getFile(id)?.data === file.data) {
            uppy.emit('upload-success', file, {status: 200, body: receipt});
          }
        } catch {
          // Old servers, unavailable preflight or a failed local read use TUS as usual.
        } finally {
          clearTimeout(timeout);
          if (preflights.get(id) === controller) preflights.delete(id);
        }
      }
    }
    await Promise.all(Array.from({length: Math.min(ids.length, 3)}, checkFiles));
  });
  uppy.on('file-removed', file => preflights.get(file.id)?.abort());
  uppy.on('state-update', (_, state) => {if(alive)onchange(Object.values(state.files).map(file => ({
    id:file.id,name:file.name,pct:Math.round(file.progress.percentage || 0),error:file.error || '',
    quotaError:typeof file.error === 'string' && file.error.includes('ERR_PERSONAL_QUOTA'),
    paused:file.isPaused,done:file.progress.uploadComplete,started:!!file.progress.uploadStarted,
    source:file.meta.source,path:file.meta.path,size:file.size,uploaded:file.progress.bytesUploaded || 0
  })));});
  uppy.on('upload-success', file => {if(alive)oncomplete(file);});
  uppy.on('complete', ({successful = []}) => {
    if (!alive) return;
    for (const file of successful) {
      const current = uppy.getFile(file.id);
      if (current?.progress.uploadComplete && current.data === file.data) uppy.removeFile(file.id);
    }
  });
  return {
    account,
    add(files,source,basePath) {
      let rejected = 0;
      for (const selection of files) {
        const file = selection.file ?? selection;
        const path = uploadParentPath(basePath, file, selection.relativePath);
        if (path === null) {
          rejected++;
          continue;
        }
        const id=uploadFingerprint(file,account,source,path);
        const previous=uppy.getFile(id);
        if(previous && !previous.progress.uploadComplete)continue;
        if(previous)uppy.removeFile(id);
        try {
          uppy.addFile({id,name:file.name,type:file.type,data:file,meta:{filename:file.name,source,path,uploadKey:id}});
        } catch {
          rejected++;
        }
      }
      return rejected;
    },
    toggle:id => uppy.pauseResume(id), retry:id => uppy.retryUpload(id),
    retryFailed:() => uppy.retryAll(),
    remove:id => uppy.removeFile(id),
    destroy() {alive=false;for(const controller of preflights.values())controller.abort();uppy.cancelAll();uppy.destroy();}
  };
}
