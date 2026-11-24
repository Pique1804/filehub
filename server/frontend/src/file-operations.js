export function operationSkipReason(item, destination) {
  if (item.source !== destination.source) return '';
  const separator = item.path.lastIndexOf('/');
  const parent = separator === -1 ? '' : item.path.slice(0, separator);
  if (parent === destination.path) return 'operationSameFolder';
  if (item.isDir && (destination.path === item.path || destination.path.startsWith(item.path + '/'))) {
    return 'operationInsideFolder';
  }
  return '';
}

function validPart(part) {
  return part && part !== '.' && part !== '..' && !part.startsWith('.filehub-') && !/[\\/\0]/.test(part);
}

export function uploadParentPath(base, file, relativePath = file.webkitRelativePath) {
  if (!relativePath) return base;
  const parts = relativePath.split('/');
  const name = parts.pop();
  if (name !== file.name || !parts.length || !parts.every(validPart)) {
    return null;
  }
  return [base, ...parts].filter(Boolean).join('/');
}

// Read each chunk before handing it to XHR. Disk-backed drag/paste files can
// become unreadable, and a lazy Blob can otherwise fail mid-request.
export const uploadFileReader = {
  async openFile(file) {
    return {
      size: file.size,
      async slice(start, end) {
        try {
          const bytes = await file.slice(start, end).arrayBuffer();
          if (bytes.byteLength !== end - start) throw new Error('incomplete file read');
          return {value: new Blob([bytes]), done: end >= file.size};
        } catch (cause) {
          throw new Error('ERR_FILEHUB_LOCAL_READ', {cause});
        }
      },
      close() {},
    };
  },
};

export function readClipboardFiles(transfer) {
  const files = Array.from(transfer?.files ?? []);
  return files.length ? files : Array.from(transfer?.items ?? [])
    .filter(item => item.kind === 'file').map(item => item.getAsFile()).filter(Boolean);
}

export function readDroppedTree(transfer) {
  // The browser only exposes the original items during the drop event.
  const items = Array.from(transfer.items ?? []).filter(item => item.kind === 'file');
  const captured = items.length
    ? items.map(item => {
      const entry = item.webkitGetAsEntry?.();
      if (entry?.isDirectory) return {entry};
      const file = item.getAsFile();
      return file ? {file} : {entry};
    })
    : Array.from(transfer.files, file => ({file}));
  return collectEntries(captured);
}

async function collectEntries(captured) {
  const files = [], emptyDirectories = [];
  let rejected = 0;

  async function visit(entry, parent) {
    try {
      if (!validPart(entry.name)) throw new Error();
      const path = parent ? parent + '/' + entry.name : entry.name;
      if (entry.isFile) {
        const file = await new Promise((resolve, reject) => entry.file(resolve, reject));
        files.push({file, relativePath:parent ? path : ''});
      } else if (entry.isDirectory) {
        const reader = entry.createReader();
        let hasChildren = false;
        while (true) {
          const entries = await new Promise((resolve, reject) => reader.readEntries(resolve, reject));
          if (!entries.length) break;
          hasChildren = true;
          for (const child of entries) await visit(child, path);
        }
        if (!hasChildren) emptyDirectories.push(path);
      }
    } catch {
      rejected++;
    }
  }

  for (const {entry, file} of captured) {
    if (entry) await visit(entry, '');
    else if (file) files.push({file, relativePath:''});
    else rejected++;
  }
  return {files, emptyDirectories, rejected};
}
