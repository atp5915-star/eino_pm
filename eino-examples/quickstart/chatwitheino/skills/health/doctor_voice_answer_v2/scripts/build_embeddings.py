#!/usr/bin/env python3
"""Offline: turn data/label_corpus.json into data/embeddings.json.

Each label gets a "semantic profile" string = name + definition + 3 examples,
embedded once via OneAPI. Output is loaded by tools/classify_cell.go at runtime.

Usage:
  EMBED_API_KEY=sk-xxx python3 scripts/build_embeddings.py

Defaults to OneAPI gateway (oneapi-comate.baidu-int.com) with text-embedding-v3.
"""
import json, os, sys, time, urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CORPUS = ROOT / 'data' / 'label_corpus.json'
OUT = ROOT / 'data' / 'embeddings.json'

BASE_URL = os.environ.get('EMBED_BASE_URL', 'https://oneapi-comate.baidu-int.com')
API_KEY = os.environ.get('EMBED_API_KEY', '')
MODEL   = os.environ.get('EMBED_MODEL', 'text-embedding-v3')

if not API_KEY:
    print('ERROR: set EMBED_API_KEY', file=sys.stderr); sys.exit(1)


def profile(name, definition, examples):
    ex = '；'.join(examples) if examples else ''
    return f'{name}。{definition}。代表性问题：{ex}'


def embed(text, retries=3):
    body = json.dumps({'model': MODEL, 'input': text}).encode()
    req = urllib.request.Request(
        f'{BASE_URL}/v1/embeddings', data=body,
        headers={'Authorization': f'Bearer {API_KEY}', 'Content-Type': 'application/json'},
        method='POST',
    )
    last = None
    for k in range(retries):
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                payload = json.loads(r.read())
            return payload['data'][0]['embedding']
        except Exception as e:
            last = e; time.sleep(1.5 * (k + 1))
    raise last


def main():
    corpus = json.loads(CORPUS.read_text(encoding='utf-8'))
    out = {'model': MODEL, 'topics': {}, 'intents': {}}
    for code, info in corpus['topics'].items():
        text = profile(info['name'], info['definition'], info.get('examples', []))
        vec = embed(text)
        out['topics'][code] = {'profile': text, 'vector': vec}
        print(f'  {code} {info["name"]}: dim={len(vec)}')
    for code, info in corpus['intents'].items():
        text = profile(info['name'], info['definition'], info.get('examples', []))
        vec = embed(text)
        out['intents'][code] = {'profile': text, 'vector': vec}
        print(f'  {code} {info["name"]}: dim={len(vec)}')
    OUT.write_text(json.dumps(out, ensure_ascii=False), encoding='utf-8')
    print(f'wrote {OUT} ({len(out["topics"])} topics + {len(out["intents"])} intents)')


if __name__ == '__main__':
    main()
