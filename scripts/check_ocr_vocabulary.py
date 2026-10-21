#!/usr/bin/env python3
"""Check whether the prepared DeepDoc CTC alphabet can represent a control fact.

This checks a necessary condition only; it does not prove recognition quality.
Never edit a vocabulary independently of the matching recognition model.
"""
import argparse
import hashlib
import json
from pathlib import Path


def inspect_vocabulary(path, text):
    data = Path(path).read_bytes()
    tokens = data.decode('utf-8').splitlines()
    alphabet = set(''.join(tokens) + ' ')
    missing = sorted(set(text) - alphabet)
    return {
        'sha256': hashlib.sha256(data).hexdigest(),
        'tokens': len(tokens),
        'decoder_classes': len(tokens) + 2,
        'required_text': text,
        'missing_characters': missing,
        'representable': not missing,
        'cyrillic_tokens': sum(any('\u0400' <= c <= '\u052f' for c in t) for t in tokens),
        'scope': 'alphabet coverage only; no inference, retrieval or citations tested',
    }


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('vocabulary', type=Path)
    parser.add_argument('--text', default='Нева-31')
    args = parser.parse_args()
    result = inspect_vocabulary(args.vocabulary, args.text)
    print(json.dumps(result, ensure_ascii=False, indent=2))
    raise SystemExit(0 if result['representable'] else 1)
