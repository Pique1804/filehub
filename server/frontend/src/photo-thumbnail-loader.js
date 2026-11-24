// The virtualizer keeps several extra rows mounted. Request only nearby
// tiles so a fast scroll can move work to the new viewport.
export function createPhotoThumbnailLoader() {
  const tiles = new Map();
  let observer;
  let active = 0;
  let activeVideos = 0;

  function pump() {
    for (const tile of tiles.values()) {
      if (active >= 6) break;
      if (!tile.nearby || tile.finished || tile.request || tile.retry) continue;
      if (tile.video && activeVideos >= 1) continue;
      void load(tile);
    }
  }

  async function load(tile) {
    const request = new AbortController();
    const video = tile.video;
    tile.request = request;
    active++;
    if (video) activeVideos++;
    let retry = false;
    try {
      const response = await fetch(tile.url, {signal:request.signal});
      if (!response.ok) {
        retry = response.status === 503 || response.status === 429;
        return;
      }
      const blob = await response.blob();
      if (!request.signal.aborted) {
        tile.objectUrl = URL.createObjectURL(blob);
        tile.image.src = tile.objectUrl;
      }
    } catch (error) {
      retry = error instanceof TypeError;
    } finally {
      active--;
      if (video) activeVideos--;
      if (tile.request === request) {
        tile.request = null;
        if (!request.signal.aborted) {
          if (retry && tile.retries < 2) {
            tile.retries++;
            tile.retry = setTimeout(() => {
              tile.retry = null;
              pump();
            }, 1000 * tile.retries);
          } else {
            tile.finished = true;
          }
        }
      }
      pump();
    }
  }

  function suspend(tile) {
    tile.request?.abort();
    clearTimeout(tile.retry);
    tile.retry = null;
  }

  function release(tile) {
    suspend(tile);
    tiles.delete(tile.image);
    if (tile.objectUrl) {
      tile.image.removeAttribute('src');
      URL.revokeObjectURL(tile.objectUrl);
    }
  }

  return (image, settings) => {
    if (!observer) {
      observer = new IntersectionObserver(entries => {
        for (const entry of entries) {
          const tile = tiles.get(entry.target);
          if (!tile) continue;
          tile.nearby = entry.isIntersecting;
          if (!tile.nearby) suspend(tile);
        }
        pump();
      }, {root:image.closest('.timeline'), rootMargin:'72px 0px'});
    }
    let tile = {image, ...settings, retries:0};
    tiles.set(image, tile);
    observer.observe(image);
    return {
      update(next) {
        if (next.url === tile.url && next.video === tile.video) return;
        const nearby = tile.nearby;
        release(tile);
        tile = {image, ...next, nearby, retries:0};
        tiles.set(image, tile);
        pump();
      },
      destroy() {
        observer.unobserve(image);
        release(tile);
        if (tiles.size === 0) {
          observer.disconnect();
          observer = null;
        }
      }
    };
  };
}
