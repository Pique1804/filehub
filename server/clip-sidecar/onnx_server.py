#!/usr/bin/env python3
"""Local CPU encoder for the Q4 CLIP model evaluated in the FileHub project."""

import argparse
import io
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import re
import threading
from urllib.parse import parse_qs, urlsplit

RUSSIAN_LETTER = re.compile(r"[а-яё]", re.IGNORECASE)


class RussianQueryTranslator:
    def __init__(self, directory):
        from ctranslate2 import Translator
        from sentencepiece import SentencePieceProcessor

        self.tokenizer = SentencePieceProcessor(model_file=str(directory / "sentencepiece.model"))
        self.model = Translator(str(directory / "model"), device="cpu", compute_type="int8",
                                inter_threads=1, intra_threads=1)

    def photo_query(self, query):
        # Photo context helps with ambiguous one-word requests such as «кот».
        description = f"На фотографии {query.rstrip('.!?…')}."
        tokens = self.tokenizer.encode(description, out_type=str)
        result = self.model.translate_batch([tokens], beam_size=2, max_input_length=112,
                                            max_decoding_length=64, disable_unk=True)
        return self.tokenizer.decode(result[0].hypotheses[0]).strip() or query


class Encoder:
    def __init__(self, directory, russian_model=None):
        import numpy as np
        import onnxruntime as ort
        from PIL import Image, ImageOps
        from tokenizers import Tokenizer

        self.np = np
        self.image_module, self.image_ops = Image, ImageOps
        configuration = json.loads((directory / "preprocessor_config.json").read_text())
        self.mean = np.asarray(configuration["image_mean"], dtype=np.float32)
        self.std = np.asarray(configuration["image_std"], dtype=np.float32)
        options = ort.SessionOptions()
        options.intra_op_num_threads = options.inter_op_num_threads = 1
        self.model = ort.InferenceSession(
            str(directory / "model_q4.onnx"), options, providers=["CPUExecutionProvider"]
        )
        self.tokenizer = Tokenizer.from_file(str(directory / "tokenizer.json"))
        self.tokenizer.enable_truncation(max_length=77)
        self.tokenizer.enable_padding(length=77, pad_id=0, pad_token="!")
        self.empty_frame = np.zeros((1, 3, 224, 224), dtype=np.float32)
        self.russian_model = russian_model
        self.translator = None

    def encode(self, text, frame, output):
        np = self.np
        query = self.tokenizer.encode(text)
        values = {
            "input_ids": np.asarray([query.ids], dtype=np.int64),
            "attention_mask": np.asarray([query.attention_mask], dtype=np.int64),
            "pixel_values": frame,
        }
        result = self.model.run([output], {input.name: values[input.name] for input in self.model.get_inputs()})[0][0]
        norm = float(np.linalg.norm(result))
        if not np.isfinite(norm) or norm <= 0:
            raise ValueError("invalid embedding")
        return (result / norm).tolist()

    def text(self, query):
        if self.russian_model is not None and RUSSIAN_LETTER.search(query):
            if self.translator is None:
                self.translator = RussianQueryTranslator(self.russian_model)
            query = self.translator.photo_query(query)
        return self.encode(query, self.empty_frame, "text_embeds")

    def image(self, data):
        np = self.np
        with self.image_module.open(io.BytesIO(data)) as source:
            if source.format != "JPEG" or source.width * source.height > 2048 * 2048:
                raise ValueError("invalid thumbnail")
            image = self.image_ops.fit(source.convert("RGB"), (224, 224), self.image_module.Resampling.BICUBIC)
        frame = (np.asarray(image, dtype=np.float32) / 255 - self.mean) / self.std
        return self.encode("a photo", np.ascontiguousarray(frame.transpose(2, 0, 1)[None]), "image_embeds")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--model-dir", type=Path, required=True)
    parser.add_argument("--ru-en-dir", type=Path, help="optional local Argos Russian-to-English package")
    parser.add_argument("--bind", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8901)
    parser.add_argument("--client", action="append", default=[])
    args = parser.parse_args()
    for asset in ("model_q4.onnx", "tokenizer.json", "preprocessor_config.json"):
        if not (args.model_dir / asset).is_file():
            parser.error(f"missing model asset: {asset}")
    if args.ru_en_dir is not None:
        for asset in ("sentencepiece.model", "model/model.bin", "model/config.json"):
            if not (args.ru_en_dir / asset).is_file():
                parser.error(f"missing translation model asset: {asset}")

    allowed = {"127.0.0.1", "::1", *args.client}
    slot = threading.Lock()
    model = None

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass  # Searches and image bodies are private.

        def respond(self, status, payload):
            body = json.dumps(payload, allow_nan=False).encode()
            try:
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)
            except (BrokenPipeError, ConnectionResetError):
                pass

        def process(self, payload, *, image=False):
            nonlocal model
            if not slot.acquire(blocking=False):
                self.respond(503, {"ok": False})
                return
            try:
                if model is None:
                    model = Encoder(args.model_dir, args.ru_en_dir)
                vector = model.image(payload) if image else model.text(payload)
                self.respond(200, {"vec": vector})
            except (OSError, ValueError):
                self.respond(400, {"ok": False})
            except Exception:
                self.respond(503, {"ok": False})
            finally:
                slot.release()

        def do_GET(self):
            if self.client_address[0] not in allowed:
                self.respond(403, {"ok": False})
                return
            query = urlsplit(self.path)
            if query.path == "/health":
                self.respond(200, {"ok": True, "model": "ViT-B-32"})
            elif query.path == "/embed_text":
                text = parse_qs(query.query).get("q", [""])[0].strip()
                if not text or len(text) > 2000:
                    self.respond(400, {"ok": False})
                else:
                    self.process(text)
            else:
                self.respond(404, {"ok": False})

        def do_POST(self):
            if self.client_address[0] not in allowed:
                self.respond(403, {"ok": False})
                return
            if self.path != "/embed_image":
                self.respond(404, {"ok": False})
                return
            try:
                count = int(self.headers.get("Content-Length", "0"))
            except ValueError:
                count = 0
            if not 0 < count <= 4 * 1024 * 1024:
                self.respond(413, {"ok": False})
                return
            self.connection.settimeout(5)
            try:
                image = self.rfile.read(count)
            except OSError:
                self.respond(400, {"ok": False})
                return
            if len(image) != count:
                self.respond(400, {"ok": False})
                return
            self.process(image, image=True)

    ThreadingHTTPServer((args.bind, args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
