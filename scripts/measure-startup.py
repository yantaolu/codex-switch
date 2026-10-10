#!/usr/bin/env python3
"""Measure credential-free CLI startup; does not switch accounts or access APIs."""
import argparse
import json
import os
from pathlib import Path
import statistics
import subprocess
import time

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('binary', type=Path)
parser.add_argument('--runs', type=int, default=40)
parser.add_argument('--output', type=Path)
args = parser.parse_args()
if args.runs < 20:
    parser.error('--runs must be at least 20')
binary = args.binary.resolve()
commands = {}
for option in ('version', '--help'):
    for _ in range(5):
        subprocess.run([str(binary), option], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True)
    values = []
    for _ in range(args.runs):
        started = time.perf_counter_ns()
        subprocess.run([str(binary), option], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True)
        values.append((time.perf_counter_ns() - started) / 1e6)
    ordered = sorted(values)
    commands[option] = dict(runs=args.runs, median_ms=round(statistics.median(values), 3),
                           p95_ms=round(ordered[(95 * args.runs + 99) // 100 - 1], 3),
                           min_ms=round(min(values), 3), max_ms=round(max(values), 3))
counts = {key: dict(files=0, lines=0) for key in ('production', 'tests')}
for path in [*Path('cmd').rglob('*.go'), *Path('internal').rglob('*.go')]:
    item = counts['tests' if path.name.endswith('_test.go') else 'production']
    item['files'] += 1
    item['lines'] += len(path.read_text().splitlines())
result = dict(commands=commands, binary_bytes=binary.stat().st_size, source=counts,
              version=subprocess.check_output([str(binary), 'version'], text=True).strip(),
              go=subprocess.check_output(['go', 'version'], text=True).strip(),
              macos=subprocess.check_output(['sw_vers', '-productVersion'], text=True).strip(), arch=os.uname().machine)
output = json.dumps(result, ensure_ascii=False, indent=2) + '\n'
if args.output:
    args.output.write_text(output)
print(output, end='')
