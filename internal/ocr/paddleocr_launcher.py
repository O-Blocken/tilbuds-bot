"""Run the tjekapi PaddleOCR worker while filtering native stdout noise.

PaddlePaddle can write diagnostic lines directly to the process stdout file
descriptor. The tjekapi worker uses stdout for newline-delimited JSON, so this
launcher forwards only the worker's JSON protocol messages and sends all other
child output to stderr.
"""

import json
import os
import subprocess
import sys


_PROTOCOL_KEYS = ("ready", "ok")


def _is_protocol_message(line):
    try:
        payload = json.loads(line)
    except json.JSONDecodeError:
        return False
    return isinstance(payload, dict) and any(key in payload for key in _PROTOCOL_KEYS)


def main():
    environment = os.environ.copy()
    environment["PYTHONUTF8"] = "1"
    environment["PYTHONIOENCODING"] = "utf-8"
    child = subprocess.Popen(
        [sys.executable, *sys.argv[1:]],
        stdout=subprocess.PIPE,
        stderr=sys.stderr,
        env=environment,
        text=True,
        encoding="utf-8",
        bufsize=1,
    )
    assert child.stdout is not None
    for line in child.stdout:
        if _is_protocol_message(line):
            sys.stdout.write(line)
            sys.stdout.flush()
        else:
            print(f"filtered PaddleOCR stdout: {line.rstrip()}", file=sys.stderr, flush=True)
    return child.wait()


if __name__ == "__main__":
    raise SystemExit(main())
