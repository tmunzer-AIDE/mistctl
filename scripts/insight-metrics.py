#!/usr/bin/env python3
"""Rebuild internal/cli/insight_metrics.json, the metric catalog mistctl embeds.

It reads the Mist metric definitions (GET /api/v1/const/insight_metrics) on
stdin and keeps what `mistctl insights` needs: the scopes, type, unit,
intervals, parameters and series of each metric.

    mistctl insights metrics --live -o json | scripts/insight-metrics.py \
        > internal/cli/insight_metrics.json
"""
import json
import sys

# The scopes `mistctl insights` can query.
SCOPES = {"site", "client", "ap", "device", "switch", "gateway", "mxedge"}


def main():
    data = json.load(sys.stdin)
    if isinstance(data, dict) and isinstance(data.get("data"), dict):
        data = data["data"]  # wrapped answer
    out = {}
    for name, m in sorted(data.items()):
        if "{" in name or not isinstance(m, dict):
            continue  # templated names ({ctype}-visit) need a substitution
        scopes = sorted(s for s in m.get("scopes") or [] if s in SCOPES)
        if not scopes:
            continue
        e = {"type": m.get("type") or "", "scopes": scopes}
        if m.get("unit"):
            e["unit"] = m["unit"]
        if m.get("description"):
            e["desc"] = " ".join(m["description"].split())
        iv = {
            k: [v["interval"], v["max_age"]]
            for k, v in (m.get("intervals") or {}).items()
            if isinstance(v, dict) and "interval" in v and "max_age" in v
        }
        if iv:
            e["intervals"] = iv
        params = {}
        for k, v in (m.get("params") or {}).items():
            p = {}
            if isinstance(v, dict):
                if v.get("required"):
                    p["required"] = True
                if v.get("choices"):
                    p["choices"] = [str(c) for c in v["choices"]]
            params[k] = p
        if params:
            e["params"] = params
        keys = m.get("keys")
        if isinstance(keys, dict):
            k = {n: (v.get("unit") or "") if isinstance(v, dict) else "" for n, v in keys.items() if not n.startswith(":")}
            if k:
                e["keys"] = k
        out[name] = e
    json.dump(out, sys.stdout, indent=1, sort_keys=True)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
