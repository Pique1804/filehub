import argparse
from pathlib import Path
import json
import resource
import sqlite3
import statistics
import time

import numpy as np
import onnxruntime as ort
from tokenizers import Tokenizer

parser = argparse.ArgumentParser(description="Measure local retrieval on synthetic FileHub examples.")
parser.add_argument("--model", type=Path, required=True)
parser.add_argument("--tokenizer", type=Path, required=True)
parser.add_argument("--revision", default="unspecified")
args = parser.parse_args()
documents = [
    ('kitchen','Смета на демонтаж шкафов, замену столешницы и покраску стен кухни.'),
    ('garage','Смета на замену крыши гаража, листовой металл и водосток.'),
    ('winter','Winter tyre replacement invoice and vehicle service record.'),
    ('flight','Flight booking confirmation and electronic boarding ticket.'),
    ('school','Расписание уроков, школьная форма и родительское собрание.'),
    ('plant','Garden irrigation timer instructions for greenhouse tomatoes.'),
    ('pet','Dog vaccination passport and veterinary booster appointment.'),
    ('work','Quarterly financial report and forecast for customer renewals.'),
    ('lease','Квитанция об оплате аренды квартиры за март и банковские реквизиты.'),
    ('birthday','Заказ торта с доставкой на день рождения и число гостей.'),
    ('water','Kitchen sink installation manual with water supply fittings.'),
    ('bike','Инструкция по настройке тормозов и цепи велосипеда.'),
]
# Each image record has separately supplied OCR (left) and a manually declared
# description (right). It measures retrieval, without pretending to measure
# caption generation or CLIP on real pixels.
photos = [
    ('snowdog','', 'Собака с рыжим ошейником бежит по снегу среди елей.'),
    ('beachdog','', 'Dog running on a beach beside a blue ocean in summer.'),
    ('snowcar','', 'Синий автомобиль покрыт снегом на парковке.'),
    ('river','', 'Bridge over a quiet river with trees in autumn.'),
    ('sunset','', 'Fishing boat in a harbour at an orange sunset.'),
    ('receipt','КАФЕ ЧЕК КОФЕ 250 РУБ', 'Бумажный чек за кофе на столике в кафе.'),
    ('concert','JAZZ FESTIVAL SATURDAY', 'Плакат джазового фестиваля на стене перед концертным залом.'),
    ('bread','', 'Свежий круглый хлеб на доске рядом с мукой.'),
    ('mountain','', 'People with hiking backpacks walking uphill in the mountains.'),
    ('cycle','', 'Велосипед прислонён к дереву возле парковой скамейки.'),
    ('office','', 'Three colleagues in an office in front of a projection screen.'),
    ('birthdayphoto','С ДНЁМ РОЖДЕНИЯ', 'Торт со свечами и дети на дне рождения.'),
]
cases = [
    ('doc','ru','документ о ремонте кухни','kitchen'),
    ('doc','en','renovation estimate for the kitchen','kitchen'),
    ('doc','ru','билеты на самолёт','flight'),
    ('doc','en','proof of rent payment in March','lease'),
    ('doc','ru','как поливать теплицу','plant'),
    ('doc','en','bike brake adjustment','bike'),
    ('doc','ru','смета на замену крыши гаража','garage'),
    ('doc','en','Winter tyre replacement','winter'),
    ('photo','ru','фото с собакой на снегу','snowdog'),
    ('photo','en','a dog in a snowy forest','snowdog'),
    ('photo','ru','лодка в порту на закате','sunset'),
    ('photo','en','people hiking in the mountains','mountain'),
    ('photo','ru','торт со свечами у детей','birthdayphoto'),
    ('photo','en','concert poster for a jazz festival','concert'),
    ('photo','ru','ЧЕК КОФЕ','receipt'),
    ('photo','en','a dog running at the beach','beachdog'),
]

description_rows = [('doc', key, text,text) for key,text in documents] + [('photo', key, ocr,text) for key,ocr,text in photos]
db = sqlite3.connect(':memory:')
db.execute("CREATE VIRTUAL TABLE baseline USING fts5(body, kind UNINDEXED, id UNINDEXED, tokenize='unicode61 remove_diacritics 2')")
start = time.perf_counter()
db.executemany('INSERT INTO baseline VALUES(?,?,?)', [(ocr,kind,key) for kind,key,ocr,_ in description_rows])
fts_index_ms = (time.perf_counter()-start)*1000
fts_bytes = db.execute('PRAGMA page_size').fetchone()[0]*db.execute('PRAGMA page_count').fetchone()[0]
def lexical(kind, query):
    terms = ' '.join('"' + word.replace('"','""') + '"*' for word in query.split())
    return [key for key, in db.execute(
        'SELECT id FROM baseline WHERE baseline MATCH ? AND kind=? ORDER BY bm25(baseline) LIMIT 5',
        (terms,kind),
    )]

start = time.perf_counter()
config = ort.SessionOptions()
config.intra_op_num_threads = config.inter_op_num_threads = 1
session = ort.InferenceSession(str(args.model),config,providers=['CPUExecutionProvider'])
tokenizer = Tokenizer.from_file(str(args.tokenizer))
tokenizer.enable_padding()
tokenizer.enable_truncation(max_length=128)
inputs = {item.name for item in session.get_inputs()}
startup_ms = (time.perf_counter()-start)*1000

def encode(prefix, texts):
    tokens = tokenizer.encode_batch([prefix + text for text in texts])
    mask = np.asarray([value.attention_mask for value in tokens], dtype=np.int64)
    fields = {
        'input_ids':np.asarray([value.ids for value in tokens],dtype=np.int64),
        'attention_mask':mask,
        'token_type_ids':np.asarray([value.type_ids for value in tokens],dtype=np.int64),
    }
    values = session.run(None,{key:value for key,value in fields.items() if key in inputs})[0]
    vectors = (values*mask[...,None]).sum(axis=1)/mask.sum(axis=1,keepdims=True)
    normalized = vectors/np.linalg.norm(vectors,axis=1,keepdims=True)
    return normalized.astype(np.float32)

started = time.perf_counter()
vectors = np.concatenate([encode('passage: ',[r[3] for r in description_rows[i:i+4]]) for i in range(0,len(description_rows),4)])
index_ms = (time.perf_counter()-started)*1000
latencies = {'fts':[], 'embedding':[]}
quality = []
for kind,language,query,relevant in cases:
    started = time.perf_counter()
    fts = lexical(kind,query)
    latencies['fts'].append((time.perf_counter()-started)*1000)
    started = time.perf_counter()
    qv = encode('query: ',[query])[0]
    scores = vectors @ qv
    order = sorted((i for i,row in enumerate(description_rows) if row[0]==kind),
        key=lambda i: float(scores[i]),reverse=True)
    retrieved = [description_rows[i][1] for i in order[:5]]
    latencies['embedding'].append((time.perf_counter()-started)*1000)
    quality.append(dict(kind=kind, language=language, query=query, expected=relevant, fts=fts, e5=retrieved))
result = dict(
    model_revision=args.revision,
    model_bytes=args.model.stat().st_size,
    tokenizer_bytes=args.tokenizer.stat().st_size,
    startup_ms=round(startup_ms,2),index_ms=round(index_ms,2),fts_index_ms=round(fts_index_ms,2),
    vectors_bytes=vectors.nbytes,fts_sqlite_bytes=fts_bytes,records=len(description_rows),
    peak_rss_mib=round(resource.getrusage(resource.RUSAGE_SELF).ru_maxrss/1024,1),
    query_ms={key:dict(median=round(statistics.median(value),2), maximum=round(max(value),2)) for key,value in latencies.items()},
    results=quality,
)
print(json.dumps(result, ensure_ascii=False, indent=2))
