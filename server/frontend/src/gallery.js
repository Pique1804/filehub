import { originalUrl, thumbUrl } from './media-utils.js';
import icons from './material-icons.json';
import 'photoswipe/style.css';

const previewUrl = photo => /\.(heic|heif)$/i.test(photo.path) ? thumbUrl(photo,2048) : originalUrl(photo);

export async function openGallery({photos, index, onshare, onalbum, canTrash, ontrash, onclose, signal, t}) {
  if(!photos.length || index<0 || index>=photos.length)throw new Error("Empty gallery");
  const {default:PhotoSwipe} = await import('photoswipe');
  if(signal?.aborted)throw new DOMException('Aborted','AbortError');
  const source = photos.map(p => ({src:previewUrl(p), msrc:thumbUrl(p), width:1600, height:1200, alt:p.name, photo:p, isVideo:p.isVideo, ...(p.isVideo ? {type:"html",html:" "} : {})}));
  const gallery = new PhotoSwipe({dataSource:source,index,bgOpacity:1,showHideAnimationType:'fade',
    closeTitle:t('close'),zoomTitle:t('galleryZoom'),arrowPrevTitle:t('galleryPrev'),arrowNextTitle:t('galleryNext'),errorMsg:t('galleryLoadError'),
    secondaryZoomLevel:2});
  gallery.on('loadComplete', ({content}) => {
    const {naturalWidth:width, naturalHeight:height} = content.element ?? {};
    if (!width || !height || (width === content.width && height === content.height)) return;

    // Nearby slides may load before PhotoSwipe creates their viewport.
    Object.assign(content.data, {width, height});
    Object.assign(content, {width, height});
    if (content.slide) {
      Object.assign(content.slide, {width, height});
      content.slide.resize();
      content.slide.updateContentSize(true);
    }
  });
  gallery.on('contentLoad',event => {
    const content=event.content;if (!content.data.isVideo)return;event.preventDefault();
    const frame=document.createElement('div');frame.className='fh-gallery-video';
    const video=document.createElement('video');video.controls=true;video.playsInline=true;video.preload='metadata';video.src=originalUrl(content.data.photo);video.poster=thumbUrl(content.data.photo,1280);frame.append(video);
    content.element=frame;content.onLoaded();
  });
  gallery.on('contentDeactivate',({content}) => content.element?.querySelector?.('video')?.pause());
  gallery.on('contentDestroy',({content}) => {const video=content.element?.querySelector?.('video');if(video){video.pause();video.removeAttribute('src');video.load();}});
  gallery.on('uiRegister',() => {
    gallery.ui.registerElement({name:'fh-name',order:8,isButton:false,appendTo:'bar',onInit:element => {
      const update=() => {element.textContent=gallery.currSlide?.data.photo?.name || '';};gallery.on('change',update);update();
    }});
    gallery.ui.registerElement({name:'fh-download',order:9,isButton:true,tagName:'button',html:t('download'),onClick:() => {const a=document.createElement('a');a.href=originalUrl(source[gallery.currIndex].photo).replace('&inline=1','');a.download='';a.click();}});
    if(onshare)gallery.ui.registerElement({name:'fh-share',order:10,isButton:true,tagName:'button',html:t('share'),onClick:() => onshare(source[gallery.currIndex].photo)});
    if(onalbum)gallery.ui.registerElement({name:'fh-album',order:11,isButton:true,tagName:'button',html:t('toAlbum'),onClick:() => {const photo=source[gallery.currIndex].photo;gallery.close();onalbum(photo);}});
    if (ontrash && photos.some(canTrash)) gallery.ui.registerElement({
      name:'fh-trash', order:12, isButton:true, title:t('moveToTrash'), ariaLabel:t('moveToTrash'),
      html:'<svg class="pswp__icn" viewBox="0 0 24 24" width="24" height="24" aria-hidden="true">' + icons.delete + '</svg>',
      onInit:element => {
        element.disabled = true;
        gallery.on('openingAnimationEnd', () => { element.disabled = false; });
        const update = () => {
          const photo = source[gallery.currIndex]?.photo;
          element.style.display = photo && canTrash(photo) ? '' : 'none';
        };
        gallery.on('change', update);
        update();
      },
      onClick:(_event, element) => {
        const photo = source[gallery.currIndex].photo;
        if (!canTrash(photo)) return;
        element.disabled = true;
        gallery.on('destroy', () => ontrash(photo));
        gallery.close();
      },
    });
  });
  const abort=()=>gallery.destroy();signal?.addEventListener('abort',abort,{once:true});
  gallery.on('destroy',() => {signal?.removeEventListener('abort',abort);onclose?.();});gallery.init();return gallery;
}
