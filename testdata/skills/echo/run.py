#!/usr/bin/env python3
"""Minimal external skill.

The openLight runtime sends a single line of JSON on stdin (the v1
request envelope) and expects a single line of JSON on stdout (the v1
response envelope). stderr is captured as logs only.
"""

import json
import sys

req = json.load(sys.stdin)

text = req["input"]["text"]

if text.strip() == "ping":
    message = "pong"
else:
    message = text.replace("echo", "").strip()

print(json.dumps({
    "ok": True,
    "message": message,
}))
