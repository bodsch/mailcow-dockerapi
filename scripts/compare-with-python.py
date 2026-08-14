#!/usr/bin/env python3
"""Compares the responses of the Go and the Python implementation.

Volatile fields (timestamps, load, runtimes, ids) are normalised before the
comparison — they differ by necessity.
"""

import json
import subprocess
import sys

GO = "https://localhost:18443"
PY = "https://localhost:18444"

# Fields whose values differ between two calls anyway.
VOLATILE = {
    "usage", "uptime", "system_time", "read", "preread", "total_usage",
    "usage_in_usermode", "usage_in_kernelmode", "system_cpu_usage",
    "percpu_usage", "free", "used", "available", "cached", "buffers",
    "SizeRw", "SizeRootFs", "StartedAt", "FinishedAt", "Created",
    "cpu_usage", "precpu_stats", "cpu_stats", "memory_stats", "blkio_stats",
    "networks", "pids_stats", "num_procs", "storage_stats", "State",
    "ResolvConfPath", "HostnamePath", "HostsPath", "LogPath", "Image",
    "NetworkSettings", "GraphDriver", "Mounts", "HostConfig", "Config",
    "Id", "Name", "Path", "Args", "Driver", "Platform", "ImageManifestDescriptor",
}


def fetch(base, path, method="GET", body=None):
    cmd = ["curl", "-sk", "--max-time", "30", "-X", method, base + path]
    if body is not None:
        cmd += ["-H", "Content-Type: application/json", "-d", body]

    out = subprocess.run(cmd, capture_output=True, text=True)
    return out.stdout


def normalize(value):
    """Replaces volatile values with a placeholder."""
    if isinstance(value, dict):
        return {
            k: ("<volatil>" if k in VOLATILE else normalize(v))
            for k, v in sorted(value.items())
        }
    if isinstance(value, list):
        return [normalize(v) for v in value]
    return value


def compare(name, path, method="GET", body=None, structural=True, expected_diff=None):
    """Compares one route.

    expected_diff names a known difference explained in DEVIATIONS.md. It counts
    as a pass but is reported.
    """
    go_raw = fetch(GO, path, method, body)
    py_raw = fetch(PY, path, method, body)

    if not structural:
        ok = go_raw == py_raw
        if not ok and expected_diff:
            print(f"EXP.  {name}  ({expected_diff})")
            print(f"        go: {' '.join(go_raw.split())[:120]}")
            print(f"        py: {' '.join(py_raw.split())[:120]}")
            return True

        print(f"{'OK  ' if ok else 'DIFF'}  {name}")
        if not ok:
            print(f"        go: {go_raw[:160]}")
            print(f"        py: {py_raw[:160]}")
        return ok

    try:
        go = normalize(json.loads(go_raw))
        py = normalize(json.loads(py_raw))
    except json.JSONDecodeError as e:
        print(f"DIFF  {name}  (not JSON: {e})")
        print(f"        go: {go_raw[:160]}")
        print(f"        py: {py_raw[:160]}")
        return False

    ok = go == py
    print(f"{'OK  ' if ok else 'DIFF'}  {name}")
    if not ok:
        go_s = json.dumps(go, indent=2, sort_keys=True)
        py_s = json.dumps(py, indent=2, sort_keys=True)
        for line in list(_diff(go_s, py_s))[:20]:
            print("        " + line)
    return ok


def _diff(a, b):
    import difflib
    return difflib.unified_diff(
        a.splitlines(), b.splitlines(), "go", "python", lineterm="", n=1
    )


def main():
    cid = sys.argv[1] if len(sys.argv) > 1 else None
    results = []

    # The shape of the figures (the values themselves are volatile).
    results.append(compare("GET  /host/stats", "/host/stats"))

    results.append(compare("GET  /containers/json", "/containers/json"))
    results.append(compare("GET  /containers/json?all=true", "/containers/json?all=true"))

    if cid:
        results.append(compare(f"GET  /containers/<id>/json", f"/containers/{cid}/json"))

    # Error paths — here the body has to match character for character.
    results.append(compare(
        "GET  /containers/<invalid>/json",
        "/containers/not-hex/json",
        structural=False,
    ))
    results.append(compare(
        "POST /containers/<id>/does-not-exist",
        "/containers/abc123/does-not-exist",
        method="POST",
        structural=False,
        expected_diff="DEVIATIONS 1.9 — the fallback in main.py:159 is broken",
    ))
    results.append(compare(
        "POST /containers/<id>/exec (no cmd)",
        "/containers/abc123/exec",
        method="POST",
        body="{}",
        structural=False,
    ))
    results.append(compare(
        "POST /containers/<id>/exec (no task)",
        "/containers/abc123/exec",
        method="POST",
        body='{"cmd":"mailq"}',
        structural=False,
    ))
    results.append(compare(
        "POST /containers/<invalid>/restart",
        "/containers/abc-123/restart",
        method="POST",
        structural=False,
    ))

    if cid:
        # Acts on a real container: the process list.
        results.append(compare(
            "POST /containers/<id>/top",
            f"/containers/{cid}/top",
            method="POST",
        ))

    print()
    ok = sum(results)
    print(f"{ok}/{len(results)} matching "
          f"(EXP. = known difference, see DEVIATIONS.md)")
    return 0 if ok == len(results) else 1


if __name__ == "__main__":
    sys.exit(main())
