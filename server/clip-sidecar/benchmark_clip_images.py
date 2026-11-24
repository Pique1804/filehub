import argparse
import json
from pathlib import Path
import resource
import statistics
import time

import numpy as np
import onnxruntime as ort
from PIL import Image, ImageDraw
from tokenizers import Tokenizer

parser = argparse.ArgumentParser()
parser.add_argument("model", type=Path)
parser.add_argument("tokenizer", type=Path)
parser.add_argument("processor", type=Path)
args = parser.parse_args()

def scene(name):
    palette = {
        'snowdog':('#9eb6c4','#f4f9ff'), 'beachdog':('#3b97d6','#edce97'),
        'snowcar':('#aec7d9','#f6fbff'), 'river':('#80abd0','#bb9454'),
        'sunset':('#ee794b','#363569'), 'receipt':('#95663a','#976e43'),
        'concert':('#34271d','#524236'), 'bread':('#8c572e','#a66d3f'),
        'mountain':('#91bada','#707b62'), 'cycle':('#98bd98','#9baa75'),
        'office':('#e4e9f0','#918d81'), 'birthdayphoto':('#fae0cb','#c1a19c'),
    }
    sky, ground = palette[name]
    im = Image.new("RGB", (224,224), sky)
    d = ImageDraw.Draw(im)
    d.rectangle((0,124,224,224),fill=ground)
    if name in ('snowdog','beachdog'):
        if name=='snowdog':
            for x in (12,45,190,212):
                d.polygon([(x-18,129),(x,49),(x+22,129)], fill='#315344')
        d.ellipse((61,117,152,163),fill='#824219')
        d.ellipse((132,95,172,139),fill='#a0592d')
        d.polygon([(140,106),(146,74),(157,107)],fill='#63301a')
        d.ellipse((160,104,166,110),fill='black')
        d.line([(66,138),(38,112),(30,121)],fill='#824219',width=8)
        for x in (75,102,131,148):
            d.line([(x,149),(x-10,181)],fill='#733a21',width=7)
        d.line([(139,125),(146,139)],fill='#e02d1a',width=5)
    elif name=='snowcar':
        d.rounded_rectangle((28,112,192,166),14,fill='#24518a')
        d.polygon([(50,115),(77,91),(141,91),(168,115)],fill='#dce6ee')
        for x in (49,150):
            d.ellipse((x,145,x+26,174),fill='#171922')
    elif name=='river':
        d.rectangle((0,115,224,180),fill='#346f9c')
        d.arc((20,80,204,178),180,360,fill='#65452f',width=9)
        d.line([(22,119),(201,119)],fill='#6d4e39',width=7)
    elif name=='sunset':
        d.ellipse((94,65,159,129),fill='#ffbe49')
        d.polygon([(32,151),(162,151),(142,172),(54,171)],fill='#13132b')
        d.line([(85,150),(85,94)],fill='#242033',width=4)
    elif name=='receipt':
        d.rectangle((48,25,176,199),fill='#faf4e9')
        for y, line in zip((55,82,105,142),('CAFE','COFFEE','TOTAL 250','THANK YOU')):
            d.text((64,y),line,fill='black')
    elif name=='concert':
        d.rounded_rectangle((35,22,190,200),6,fill='#f3c04b')
        d.text((72,69),'JAZZ',fill='#151735')
        d.text((71,108),'FESTIVAL',fill='#151735')
        d.text((65,149),'SATURDAY',fill='#151735')
    elif name=='bread':
        d.ellipse((39,62,181,165),fill='#5a2610')
        d.ellipse((41,56,183,151),fill='#c98945')
        for x in (62,92,124):
            d.arc((x,84,x+34,127),189,322,fill='#f9db8d',width=5)
    elif name=='mountain':
        for vertices in ([(0,144),(76,40),(157,146)],[(87,143),(163,27),(224,147)]):
            d.polygon(vertices,fill='#6a7b9c')
            x,y=vertices[1]
            d.polygon([(x-15,y+25),(x,y),(x+17,y+26)],fill='white')
        for x in (75,125):
            d.ellipse((x,143,x+10,153),fill='#f0bc80')
            d.line((x+4,153,x+7,180),fill='#a84832',width=7)
    elif name=='cycle':
        for x in (42,138):
            d.ellipse((x,125,x+42,167),outline='#151817',width=4)
        d.line([(63,144),(97,114),(105,143),(63,144),(103,112),(156,144)], fill='#a44237',width=4)
    elif name=='office':
        d.rectangle((67,32,181,100),fill='#fbfbfe',outline='#273746',width=3)
        d.polygon([(55,147),(173,147),(194,204),(31,204)],fill='#774d35')
        for x in (27,86,164):
            d.ellipse((x,118,x+16,136),fill='#ad764b')
            d.rectangle((x+2,136,x+15,158),fill='#315473')
    elif name=='birthdayphoto':
        d.rounded_rectangle((52,112,171,169),12,fill='#f3a5b9')
        d.ellipse((52,101,171,129),fill='#fff1e3')
        for x in (76,103,130,154):
            d.rectangle((x,84,x+4,112),fill='#df4549')
            d.ellipse((x,72,x+5,84),fill='#ffbc29')
    return im

keys = ['snowdog','beachdog','snowcar','river','sunset','receipt','concert','bread','mountain','cycle','office','birthdayphoto']
cases = [('ru','фото с собакой на снегу','snowdog'),('en','a dog in a snowy forest','snowdog'),
         ('ru','лодка в порту на закате','sunset'),('en','people hiking in the mountains','mountain'),
         ('ru','торт со свечами у детей','birthdayphoto'),('en','concert poster for a jazz festival','concert'),
         ('ru','ЧЕК КОФЕ','receipt'),('en','a dog running at the beach','beachdog')]
config = json.loads(args.processor.read_text())
mean = np.asarray(config['image_mean'], dtype=np.float32)
std = np.asarray(config['image_std'], dtype=np.float32)
opts = ort.SessionOptions()
opts.intra_op_num_threads = opts.inter_op_num_threads = 1
start = time.perf_counter()
session = ort.InferenceSession(str(args.model),opts,providers=['CPUExecutionProvider'])
encoder = Tokenizer.from_file(str(args.tokenizer))
encoder.enable_truncation(max_length=77)
encoder.enable_padding(length=77,pad_id=0,pad_token='!')
names = [x.name for x in session.get_outputs()]
startup_ms = (time.perf_counter()-start)*1000

def run(queries, images):
    texts = encoder.encode_batch(queries)
    feed = {'input_ids':np.asarray([x.ids for x in texts],dtype=np.int64),
            'attention_mask':np.asarray([x.attention_mask for x in texts],dtype=np.int64),
            'pixel_values':np.stack(images)}
    outputs = dict(zip(names,session.run(None,{x.name:feed[x.name] for x in session.get_inputs()})))
    return outputs['image_embeds'], outputs['text_embeds']

pixels = [np.ascontiguousarray(((np.asarray(scene(name),dtype=np.float32)/255-mean)/std).transpose(2,0,1)) for name in keys]
start = time.perf_counter()
embedding,_ = run(['a photo'],pixels)
index_ms = (time.perf_counter()-start)*1000
results, durations = [], []
for language, query, correct in cases:
    start = time.perf_counter()
    _, text = run([query],pixels[:1])
    scores = embedding @ text[0]
    order = np.argsort(-scores)[:5]
    durations.append((time.perf_counter()-start)*1000)
    results.append(dict(language=language,query=query,expected=correct,top5=[keys[i] for i in order]))
print(json.dumps(dict(startup_ms=startup_ms,index_ms=index_ms,query_ms=statistics.median(durations),
    peak_rss_mib=resource.getrusage(resource.RUSAGE_SELF).ru_maxrss/1024,model_bytes=args.model.stat().st_size,
    photo_vector_bytes=embedding.nbytes,results=results), ensure_ascii=False, indent=2))
