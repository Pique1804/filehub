#!/usr/bin/env python3
"""Serve CLIP text vectors for Filehub from an always-on CPU host.

The GPU PC still indexes new images. This service uses the same OpenCLIP model
for queries, so the existing image vectors remain compatible when the PC sleeps.
"""

import json
import io
from PIL import Image
import os
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

import open_clip
import torch


torch.set_num_threads(2)
model, _, preprocess = open_clip.create_model_and_transforms(
    "ViT-B-32", pretrained="laion2b_s34b_b79k", device="cpu"
)
model.eval()
tokenizer = open_clip.get_tokenizer("ViT-B-32")
model_lock = threading.Lock()
allowed_client = os.environ.get("FILEHUB_CLIENT_IP", "").strip() or "127.0.0.1"


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.client_address[0] not in (allowed_client, "127.0.0.1"):
            self.send_error(403)
            return
        parsed = urlparse(self.path)
        if parsed.path == "/health":
            self.send_json({"ok": True, "model": "ViT-B-32"})
            return
        if parsed.path != "/embed_text":
            self.send_error(404)
            return
        query = parse_qs(parsed.query).get("q", [""])[0].strip()[:500]
        if not query:
            self.send_error(400)
            return
        with model_lock, torch.inference_mode():
            features = model.encode_text(tokenizer([query]))
            features /= features.norm(dim=-1, keepdim=True)
            vector = features[0].float().tolist()
        self.send_json({"vec": vector})

    def do_POST(self):
        if self.client_address[0] not in (allowed_client, "127.0.0.1"):
            self.send_error(403)
            return
        if self.path != "/embed_image":
            self.send_error(404)
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if not 0 < length <= 4 * 1024 * 1024:
                self.send_error(413)
                return
            image = Image.open(io.BytesIO(self.rfile.read(length)))
            if image.width * image.height > 2048 * 2048:
                self.send_error(413)
                return
            tensor = preprocess(image.convert("RGB")).unsqueeze(0)
            with model_lock, torch.inference_mode():
                features = model.encode_image(tensor)
                features /= features.norm(dim=-1, keepdim=True)
                vector = features[0].float().tolist()
            self.send_json({"vec": vector})
        except Exception:
            self.send_error(400)

    def send_json(self, value):
        body = json.dumps(value, separators=(",", ":")).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, format, *args):
        # URLs contain private search terms; do not place them in the journal.
        pass


if __name__ == "__main__":
    bind = os.environ.get("FILEHUB_CLIP_TEXT_BIND", "").strip() or "127.0.0.1"
    ThreadingHTTPServer((bind, 8901), Handler).serve_forever()
