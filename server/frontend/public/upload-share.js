const form = document.querySelector('form');
const picker = form.querySelector('input[type=file]');
const submit = form.querySelector('button[type=submit]');
const cancel = form.querySelector('.cancel');
const progress = form.querySelector('progress');
const status = form.querySelector('[role=status]');
const russian = document.documentElement.lang === 'ru';
const text = {
  sending: russian ? 'Файл отправляется…' : 'Sending file…',
  received: russian ? 'Файл принят.' : 'File received.',
  tooLarge: russian ? 'Файл превышает допустимый размер.' : 'This file exceeds the allowed size.',
  closed: russian ? 'Приём по этой ссылке закрыт.' : 'This link is closed for uploads.',
  conflict: russian ? 'Имя файла отклонено. Попробуйте другое имя.' : 'This filename was rejected. Try a different name.',
  unavailable: russian ? 'Файл не принят. Повторите попытку позже.' : 'Upload was not confirmed. Try again later.',
  cancelled: russian ? 'Отправка прервана.' : 'Upload cancelled.',
};

form.addEventListener('submit', event => {
  event.preventDefault();
  const file = picker.files[0];
  if (!file || submit.disabled) return;
  if (file.size > Number(form.dataset.maxBytes)) {
    status.textContent = text.tooLarge;
    return;
  }
  const xhr = new XMLHttpRequest();
  xhr.open('POST', location.pathname);
  xhr.setRequestHeader('Content-Type', 'application/octet-stream');
  xhr.setRequestHeader('X-Filehub-Filename', encodeURIComponent(file.name));
  picker.disabled = submit.disabled = true;
  cancel.hidden = progress.hidden = false;
  progress.value = 0;
  status.textContent = text.sending;
  cancel.onclick = () => xhr.abort();
  xhr.upload.addEventListener('progress', event => {
    if (event.lengthComputable && event.total > 0) progress.value = 100 * event.loaded / event.total;
  });
  xhr.addEventListener('load', () => {
    if (xhr.status === 204) {
      picker.value = '';
      status.textContent = text.received;
    } else if (xhr.status === 404 || xhr.status === 410) {
      status.textContent = text.closed;
    } else if (xhr.status === 409) {
      status.textContent = text.conflict;
    } else if (xhr.status === 413) {
      status.textContent = text.tooLarge;
    } else {
      status.textContent = text.unavailable;
    }
  });
  xhr.addEventListener('error', () => { status.textContent = text.unavailable; });
  xhr.addEventListener('abort', () => { status.textContent = text.cancelled; });
  xhr.addEventListener('loadend', () => {
    picker.disabled = submit.disabled = false;
    cancel.hidden = progress.hidden = true;
    cancel.onclick = null;
  });
  xhr.send(file);
});
