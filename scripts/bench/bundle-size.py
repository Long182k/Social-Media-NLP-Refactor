#!/usr/bin/env python3
"""Measure raw and gzip-9 compressed frontend asset bundle sizes for legacy vs v2 builds."""

from __future__ import annotations

import argparse
import gzip
from html.parser import HTMLParser
import json
from pathlib import Path
import posixpath
import re
import sys

FORBIDDEN_LEGACY_PATTERNS = (
    ("antd", re.compile(r"\bantd\b")),
    ("@ant-design", re.compile(r"@ant-design")),
    ("moment", re.compile(r"\bmoment\b")),
    ("date-fns", re.compile(r"date-fns")),
    ("apollo-client", re.compile(r"apollo-client")),
    ("@apollo/client", re.compile(r"@apollo/client")),
    ("socket.io-client", re.compile(r"socket\.io-client")),
    ("rxjs", re.compile(r"\brxjs\b")),
    ("graphql-request", re.compile(r"graphql-request")),
)


class IndexHTMLParser(HTMLParser):
    def __init__(self) -> None:
        super().__init__()
        self.entry_scripts: list[str] = []
        self.modulepreloads: list[str] = []
        self.stylesheets: list[str] = []

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        attr_map = {k.lower(): (v or "") for k, v in attrs}
        if tag.lower() == "script":
            src = attr_map.get("src", "").strip()
            typ = attr_map.get("type", "").strip().lower()
            if src and (typ == "" or typ == "module"):
                self.entry_scripts.append(src)
        elif tag.lower() == "link":
            rel_tokens = {tok.lower() for tok in attr_map.get("rel", "").split()}
            href = attr_map.get("href", "").strip()
            if not href:
                return
            if "modulepreload" in rel_tokens:
                self.modulepreloads.append(href)
            elif "stylesheet" in rel_tokens:
                self.stylesheets.append(href)


def normalize_dist_ref(ref: str) -> str | None:
    lower = ref.lower()
    if lower.startswith(("http://", "https://", "//", "data:")):
        return None
    cleaned = ref.split("?", 1)[0].split("#", 1)[0].lstrip("/")
    if cleaned.startswith("./"):
        cleaned = cleaned[2:]
    return posixpath.normpath(cleaned)


def sum_items(items: list[dict]) -> dict:
    raw = sum(item["raw_bytes"] for item in items)
    gz = sum(item["gzip_bytes"] for item in items)
    return {
        "files": [item["file"] for item in items],
        "raw_bytes": raw,
        "gzip_bytes": gz,
        "raw_kib": round(raw / 1024, 2),
        "gzip_kib": round(gz / 1024, 2),
    }


def measure_dist(dist_dir: Path, check_forbidden: bool = False) -> dict:
    assets_dir = dist_dir / "assets"
    if not assets_dir.is_dir():
        raise FileNotFoundError(f"Missing assets directory: {assets_dir}")

    files = []
    files_by_rel: dict[str, dict] = {}
    js_raw = 0
    js_gz = 0
    css_raw = 0
    css_gz = 0
    other_raw = 0
    other_gz = 0
    forbidden_hits: list[str] = []

    for path in sorted(assets_dir.rglob("*")):
        if not path.is_file():
            continue
        data = path.read_bytes()
        raw = len(data)
        gz = len(gzip.compress(data, compresslevel=9, mtime=0))
        rel = path.relative_to(dist_dir).as_posix()
        suffix = path.suffix.lower()

        if suffix in {".js", ".mjs"}:
            kind = "js"
            js_raw += raw
            js_gz += gz
        elif suffix == ".css":
            kind = "css"
            css_raw += raw
            css_gz += gz
        else:
            kind = "other"
            other_raw += raw
            other_gz += gz

        if check_forbidden and suffix in {".js", ".mjs", ".css"}:
            text = data.decode("utf-8", errors="ignore").lower()
            for marker, pattern in FORBIDDEN_LEGACY_PATTERNS:
                if pattern.search(text) and marker not in forbidden_hits:
                    forbidden_hits.append(marker)

        item = {
            "file": rel,
            "kind": kind,
            "raw_bytes": raw,
            "gzip_bytes": gz,
            "raw_kib": round(raw / 1024, 2),
            "gzip_kib": round(gz / 1024, 2),
        }
        files.append(item)
        files_by_rel[rel] = item

    index_html = dist_dir / "index.html"
    if not index_html.is_file():
        raise FileNotFoundError(f"Missing index.html: {index_html}")
    html_bytes = index_html.read_bytes()
    index_raw = len(html_bytes)
    index_gz = len(gzip.compress(html_bytes, compresslevel=9, mtime=0))

    parser = IndexHTMLParser()
    parser.feed(html_bytes.decode("utf-8", errors="replace"))

    def resolve_refs(refs: list[str]) -> list[dict]:
        seen: set[str] = set()
        resolved: list[dict] = []
        for ref in refs:
            rel = normalize_dist_ref(ref)
            if rel is None:
                continue
            if rel in seen:
                continue
            seen.add(rel)
            if rel not in files_by_rel:
                raise FileNotFoundError(f"index.html references missing asset '{rel}' in {dist_dir}")
            resolved.append(files_by_rel[rel])
        return resolved

    entry_items = resolve_refs(parser.entry_scripts)
    preloaded_items = resolve_refs(parser.modulepreloads)
    initial_css_items = resolve_refs(parser.stylesheets)

    # Deduplicate entry + preloaded in order for initial_loaded_js
    initial_js_map: dict[str, dict] = {}
    for item in entry_items + preloaded_items:
        initial_js_map[item["file"]] = item
    initial_js_items = list(initial_js_map.values())
    lazy_js_items = [
        item for item in files if item["kind"] == "js" and item["file"] not in initial_js_map
    ]

    total_raw = js_raw + css_raw + other_raw
    total_gz = js_gz + css_gz + other_gz

    return {
        "dist_dir": str(dist_dir),
        "file_count": len(files),
        "index_html": {
            "raw_bytes": index_raw,
            "gzip_bytes": index_gz,
        },
        "entry_only_js": sum_items(entry_items),
        "preloaded_js": sum_items(preloaded_items),
        "initial_loaded_js": sum_items(initial_js_items),
        "lazy_js": sum_items(lazy_js_items),
        "initial_css": sum_items(initial_css_items),
        "totals": {
            "js_raw_bytes": js_raw,
            "js_gzip_bytes": js_gz,
            "js_raw_kib": round(js_raw / 1024, 2),
            "js_gzip_kib": round(js_gz / 1024, 2),
            "css_raw_bytes": css_raw,
            "css_gzip_bytes": css_gz,
            "css_raw_kib": round(css_raw / 1024, 2),
            "css_gzip_kib": round(css_gz / 1024, 2),
            "other_raw_bytes": other_raw,
            "other_gzip_bytes": other_gz,
            "assets_raw_bytes": total_raw,
            "assets_gzip_bytes": total_gz,
            "assets_raw_kib": round(total_raw / 1024, 2),
            "assets_gzip_kib": round(total_gz / 1024, 2),
        },
        "forbidden_legacy_markers_found": forbidden_hits,
        "files": files,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="Measure legacy vs v2 frontend dist/assets sizes.")
    parser.add_argument("--legacy-dist", required=True, type=Path, help="Path to legacy FE dist directory")
    parser.add_argument("--new-dist", required=True, type=Path, help="Path to new FE dist directory")
    parser.add_argument("--json-out", type=Path, help="Optional JSON output file")
    args = parser.parse_args()

    legacy = measure_dist(args.legacy_dist, check_forbidden=False)
    new = measure_dist(args.new_dist, check_forbidden=True)

    report = {
        "legacy": legacy,
        "new": new,
    }

    if args.json_out:
        args.json_out.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")

    print("=== Frontend Bundle Size Comparison ===")
    for label, item in (("Legacy", legacy), ("New", new)):
        t = item["totals"]
        init_js = item["initial_loaded_js"]
        entry_js = item["entry_only_js"]
        print(
            f"{label:6s}: assets={t['assets_raw_kib']:.2f} KiB raw ({t['assets_gzip_kib']:.2f} KiB gz) | "
            f"total_JS={t['js_raw_kib']:.2f} KiB raw ({t['js_gzip_kib']:.2f} KiB gz) | "
            f"initial_JS={init_js['raw_kib']:.2f} KiB raw ({init_js['gzip_kib']:.2f} KiB gz) | "
            f"entry_only={entry_js['raw_kib']:.2f} KiB raw ({entry_js['gzip_kib']:.2f} KiB gz) | "
            f"CSS={t['css_raw_kib']:.2f} KiB raw ({t['css_gzip_kib']:.2f} KiB gz) | "
            f"files={item['file_count']}"
        )
    print(f"New forbidden legacy markers: {new['forbidden_legacy_markers_found']}")

    if new["forbidden_legacy_markers_found"]:
        print("ERROR: forbidden legacy markers detected in new frontend bundle!", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
