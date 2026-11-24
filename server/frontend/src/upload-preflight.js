const chunkSize = 1024 * 1024;

async function requestCheck(destination, signal) {
  const response = await fetch('/api/upload/preflight', {
    method: 'POST', signal,
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify(destination),
  });
  return response.status === 200 ? response.json() : null;
}

export async function checkExistingUpload(file, source, path, signal) {
  const destination = {source, path, filename: file.name, size: file.size};
  if ((await requestCheck(destination, signal))?.candidate !== true) return null;

  // Use bounded chunks on HTTP LAN installs as well as on HTTPS. Web Crypto's
  // digest requires a secure context and the full buffer in memory.
  const {sha256} = await import('@noble/hashes/sha256');
  const digest = sha256.create();
  for (let offset = 0; offset < file.size; offset += chunkSize) {
    signal.throwIfAborted();
    const bytes = await file.slice(offset, offset + chunkSize).arrayBuffer();
    digest.update(new Uint8Array(bytes));
  }
  const hash = Array.from(digest.digest(), byte => byte.toString(16).padStart(2, '0')).join('');
  const receipt = await requestCheck({...destination, sha256: hash}, signal);
  if (receipt?.source !== source || receipt.size !== file.size || receipt.sha256 !== hash ||
      typeof receipt.path !== 'string' || !receipt.path) return null;
  return receipt.path.slice(0, Math.max(0, receipt.path.lastIndexOf('/'))) === path ? receipt : null;
}
