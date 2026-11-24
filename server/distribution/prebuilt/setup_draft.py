"""Private, owner-scoped setup progress. Credentials are never part of a draft."""
import json
import os
from pathlib import Path
import secrets
from storage_model import Invalid


def clean(value):
    if not isinstance(value, dict) or set(value) != {'step', 'mode', 'diskChosen', 'sources', 'photoDirs'}:
        raise Invalid('Неверный формат сохранения шага.')
    if type(value['step']) is not int or not 1 <= value['step'] <= 3 or value['mode'] not in ('server', 'disk') or type(value['diskChosen']) is not bool:
        raise Invalid('Неверный шаг настройки.')
    for key, limit, fields in (('sources',16,{'name','storage','path'}), ('photoDirs',32,{'source','path','includeExisting'})):
        items = value[key]
        if not isinstance(items,list) or len(items)>limit:
            raise Invalid('Слишком много папок в настройке.')
        for item in items:
            if not isinstance(item,dict) or set(item)!=fields:
                raise Invalid('Неверная папка в сохранении шага.')
            for name, text in item.items():
                if name == 'includeExisting':
                    if type(text) is not bool:raise Invalid('Неверное подтверждение папки.')
                elif not isinstance(text,str) or len(text)>(512 if name=='path' else 128) or any(ord(c)<32 for c in text):
                    raise Invalid('Неверное имя папки в сохранении шага.')
    return json.loads(json.dumps(value))


def read(state, owner):
    path = Path(state)/'setup-draft.json'
    if not owner or not path.is_file():return None
    try:
        value=json.loads(path.read_text())
        if not isinstance(value,dict):return None
        if value.get('ownerVersion') != owner['session_version']:return None
        return clean(value['draft'])
    except (ValueError,KeyError,Invalid):
        return None


def write(state, owner, value):
    if not owner:raise Invalid('Сначала создайте аккаунт администратора.')
    draft=clean(value)
    path=Path(state)/'setup-draft.json'
    temporary=path.with_name('.draft-'+secrets.token_hex(12))
    with open(temporary,'x',opener=lambda p,f:os.open(p,f,0o600)) as stream:
        json.dump({'version':1,'ownerVersion':owner['session_version'],'draft':draft},stream,ensure_ascii=False)
        stream.flush();os.fsync(stream.fileno())
    os.replace(temporary,path)
    descriptor=os.open(path.parent,os.O_RDONLY|os.O_DIRECTORY)
    try:os.fsync(descriptor)
    finally:os.close(descriptor)
    return draft
