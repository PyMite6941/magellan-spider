#!/usr/bin/env python3
"""Talk to the Magellan Source Finder (the `api` Vercel project) from a crawl.

    python sources_client.py fetch  "<topic>" seeds.json
        GET /seeds -> seeds.json, the shape `magellan-spider -seeds` reads.
        Always writes the file (empty list on any failure) and exits 0, so a
        Source Finder outage never stops a crawl; the spider falls back to its
        built-in keyword seeds.

    python sources_client.py report "<topic>" seeds.json crawl.sq3
        POST /crawled    the leased seeds that made it into `pages`
        POST /candidates links found for this topic but never crawled, favouring
                         hosts the crawl hasn't touched (crawl-graph discovery).
                         The Source Finder verifies them before anyone crawls them.

Needs SOURCES_URL and SOURCES_KEY (env, or .env next to this file). If either
is missing, both commands are no-ops.
"""

import json
import os
import sqlite3
import sys
from collections import Counter
from urllib.parse import urlsplit

import requests

from filters import canonical_url

HERE = os.path.dirname(os.path.abspath(__file__))
MAX_FRONTIER = 40
MAX_PER_HOST = 2


def _env(name):
    val = os.environ.get(name, "")
    if val:
        return val.strip()
    path = os.path.join(HERE, ".env")
    if os.path.exists(path):
        for line in open(path, encoding="utf-8"):
            if line.startswith(name + "="):
                return line.split("=", 1)[1].strip().strip('"')
    return ""


def _conf():
    base, key = _env("SOURCES_URL").rstrip("/"), _env("SOURCES_KEY")
    if not base or not key:
        return None
    return base, {"Authorization": f"Bearer {key}"}


def fetch(topic, out_path):
    seeds = []
    conf = _conf()
    if conf:
        base, headers = conf
        try:
            r = requests.get(f"{base}/seeds", params={"topic": topic, "limit": 15},
                             headers=headers, timeout=120)
            r.raise_for_status()
            seeds = r.json().get("seeds", [])
            print(f"  sources: {len(seeds)} verified seeds")
        except (requests.RequestException, ValueError) as e:
            print(f"  sources: unavailable ({type(e).__name__}), using built-in seeds")
    with open(out_path, "w", encoding="utf-8") as f:
        json.dump({"topic": topic, "seeds": seeds}, f)


def _host(url):
    return (urlsplit(url).hostname or "").lower()


def report(topic, seeds_path, db_path):
    conf = _conf()
    if not conf or not os.path.exists(db_path):
        return
    base, headers = conf
    try:
        seeds = json.load(open(seeds_path, encoding="utf-8")).get("seeds", [])
    except (OSError, ValueError):
        seeds = []

    con = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    try:
        crawled_pages = {canonical_url(u) for (u,) in con.execute("SELECT url FROM pages")}
        seen_hosts = Counter(_host(u) for u in crawled_pages)
        frontier = [u for (u,) in con.execute(
            "SELECT url FROM crawl_queue WHERE keywords = ? AND url NOT IN (SELECT url FROM pages)",
            (topic,))]
    except sqlite3.Error as e:
        print(f"  sources: could not read {db_path}: {e}")
        return
    finally:
        con.close()

    done = [s["url"] for s in seeds if canonical_url(s["url"]) in crawled_pages]

    # Links to hosts the crawl never reached first, then the rest, with at most
    # MAX_PER_HOST per host so one site's navigation can't fill the list.
    frontier = sorted({canonical_url(u) for u in frontier if u.startswith(("http://", "https://"))},
                      key=lambda u: (seen_hosts[_host(u)] > 0, u))
    picked, per_host = [], Counter()
    for u in frontier:
        h = _host(u)
        if per_host[h] < MAX_PER_HOST:
            per_host[h] += 1
            picked.append(u)
            if len(picked) >= MAX_FRONTIER:
                break

    try:
        if done:
            requests.post(f"{base}/crawled", json={"urls": done}, headers=headers,
                          timeout=30).raise_for_status()
        if picked:
            r = requests.post(f"{base}/candidates", headers=headers, timeout=60,
                              json={"topic": topic, "urls": picked, "origin": "graph"})
            r.raise_for_status()
            print(f"  sources: {len(done)} seeds crawled, {r.json().get('queued', 0)} new candidates queued")
        else:
            print(f"  sources: {len(done)} seeds crawled, no new candidates")
    except requests.RequestException as e:
        print(f"  sources: report failed ({type(e).__name__}), continuing")


if __name__ == "__main__":
    if len(sys.argv) == 4 and sys.argv[1] == "fetch":
        fetch(sys.argv[2], sys.argv[3])
    elif len(sys.argv) == 5 and sys.argv[1] == "report":
        report(sys.argv[2], sys.argv[3], sys.argv[4])
    else:
        print(__doc__)
        sys.exit(2)
