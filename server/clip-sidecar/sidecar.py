#!/usr/bin/env python3
"""
filehub CLIP sidecar — runs on your GPU PC, gives filehub "search by meaning".

It does two jobs:
  1) INDEX (background thread): pull the list of not-yet-embedded photos from
     filehub, download each thumbnail, encode it with CLIP on the GPU, and push
     the vectors back. Resumable — safe to stop/restart, it only does what's left.
  2) SERVE (http): expose /embed_text?q=... so filehub can turn a search query
     into a vector at query time (tiny, fast, runs even on CPU).

The Go server carries no ML; all the heavy lifting is here.

Setup (Windows/Linux with an NVIDIA GPU):
    python -m venv venv && (venv\\Scripts\\activate  |  source venv/bin/activate)
    pip install torch --index-url https://download.pytorch.org/whl/cu121
    pip install open_clip_torch pillow requests

Run:
    set FILEHUB_URL=http://filehub.example.test:8080  (Linux: export ...)
    set FILEHUB_USER=admin
    set FILEHUB_PASS=your_password
    set SIDECAR_BIND=192.0.2.20
    python sidecar.py

Replace both example addresses with your own. Then on the FileHub server set
FILEHUB_CLIP_SIDECAR to this machine, e.g.
    Environment=FILEHUB_CLIP_SIDECAR=http://192.0.2.20:8900
and restart filehub. Semantic search lights up once the first vectors are in.
"""
import io
import os
import json
import time
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse, parse_qs

import requests
import torch
import open_clip
from PIL import Image

FILEHUB = os.environ.get("FILEHUB_URL", "").strip().rstrip("/")
_server_url = urlparse(FILEHUB)
if (_server_url.scheme not in ("http", "https") or not _server_url.hostname
        or _server_url.username is not None or _server_url.query or _server_url.fragment):
    raise SystemExit("Set FILEHUB_URL to your FileHub server's HTTP or HTTPS address.")
USER = os.environ.get("FILEHUB_USER", "admin")
PASS = os.environ.get("FILEHUB_PASS", "")
BIND = os.environ.get("SIDECAR_BIND", "").strip() or "127.0.0.1"
PORT = int(os.environ.get("SIDECAR_PORT", "8900"))
MODEL = "ViT-B-32"
PRETRAINED = os.environ.get("CLIP_PRETRAINED", "laion2b_s34b_b79k")
THUMB_W = 384
BATCH = 24

DEVICE = "cuda" if torch.cuda.is_available() else "cpu"
print(f"[sidecar] device={DEVICE}  model={MODEL}/{PRETRAINED}")
model, _, preprocess = open_clip.create_model_and_transforms(MODEL, pretrained=PRETRAINED, device=DEVICE)
model.eval()
tokenizer = open_clip.get_tokenizer(MODEL)

_sess = requests.Session()


def login():
    r = _sess.post(f"{FILEHUB}/api/login", data={"username": USER, "password": PASS}, timeout=10)
    r.raise_for_status()
    print(f"[sidecar] logged in as {USER}")


@torch.no_grad()
def embed_text(q: str):
    toks = tokenizer([q]).to(DEVICE)
    feats = model.encode_text(toks)
    feats /= feats.norm(dim=-1, keepdim=True)
    return feats[0].cpu().float().tolist()


@torch.no_grad()
def embed_images(imgs):
    batch = torch.stack([preprocess(im) for im in imgs]).to(DEVICE)
    feats = model.encode_image(batch)
    feats /= feats.norm(dim=-1, keepdim=True)
    return feats.cpu().float().tolist()


def fetch_thumb(source, path):
    r = _sess.get(f"{FILEHUB}/api/thumb", params={"source": source, "path": path, "w": THUMB_W}, timeout=30)
    r.raise_for_status()
    return Image.open(io.BytesIO(r.content)).convert("RGB")


def index_loop():
    while True:
        try:
            response = _sess.get(f"{FILEHUB}/api/embed/todo", params={"limit": 500}, timeout=30)
            response.raise_for_status()
            todo = response.json().get("items", [])
        except Exception as e:
            print(f"[index] todo failed: {e}; retry in 30s")
            time.sleep(30)
            continue
        if not todo:
            time.sleep(120)  # all caught up; check again later for new photos
            continue
        print(f"[index] {len(todo)} photos to embed")
        buf_items, buf_imgs = [], []

        def flush():
            if not buf_imgs:
                return True
            try:
                vecs = embed_images(buf_imgs)
                payload = {"model": MODEL, "items": [
                    {"source": it["source"], "path": it["path"], "revision": it["revision"], "vec": v}
                    for it, v in zip(buf_items, vecs)]}
                response = _sess.post(f"{FILEHUB}/api/embed/push", json=payload, timeout=60)
                response.raise_for_status()
                print(f"[index] pushed {len(buf_imgs)}")
                return True
            except Exception as e:
                print(f"[index] push failed: {e}; retry in 30s")
                time.sleep(30)
                return False
            finally:
                buf_items.clear()
                buf_imgs.clear()

        fetched = False
        for it in todo:
            try:
                buf_imgs.append(fetch_thumb(it["source"], it["path"]))
                buf_items.append(it)
                fetched = True
            except Exception as e:
                print(f"[index] skip {it.get('path')}: {e}")
                continue
            if len(buf_imgs) >= BATCH and not flush():
                break
        else:
            if fetched:
                flush()
            else:
                time.sleep(30)


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_GET(self):
        u = urlparse(self.path)
        if u.path == "/embed_text":
            q = (parse_qs(u.query).get("q") or [""])[0]
            try:
                vec = embed_text(q)
                body = json.dumps({"vec": vec}).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
            except Exception as e:
                self.send_error(500, str(e))
        elif u.path == "/health":
            self.send_response(200); self.end_headers(); self.wfile.write(b"ok")
        else:
            self.send_error(404)


if __name__ == "__main__":
    if not PASS:
        raise SystemExit("set FILEHUB_PASS")
    login()
    threading.Thread(target=index_loop, daemon=True).start()
    print(f"[sidecar] serving /embed_text on {BIND}:{PORT}")
    ThreadingHTTPServer((BIND, PORT), Handler).serve_forever()
