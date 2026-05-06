#!/usr/bin/env python3
"""Spin up a QM Cloud Sandbox desktop, print the noVNC URL, hold the
sandbox open. Convenience for manual /browserfeed smoke tests outside
the agent flow — production runs get the URL from the server response
automatically.

Pair with qmax-code's /browserfeed:

    # terminal A
    python scripts/qm_cloud_sndbx_url.py

    # copy the printed URL, then in terminal B:
    qmax-code
    > /browserfeed <url>

Ctrl+C to tear the sandbox down.

Note on env vars: the underlying SDK reads the API key from E2B_API_KEY
for legacy reasons. We accept either QMCLOUD_API_KEY (preferred) or
E2B_API_KEY (fallback), and re-export it under both names so the SDK
finds it regardless.
"""

from __future__ import annotations

import argparse
import os
import sys
import time
import urllib.error
import urllib.request

from e2b import Sandbox  # SDK module name; not re-exposed in any user surface


DEFAULT_DESKTOP_TEMPLATE = os.getenv(
    "QMCLOUD_DESKTOP_TEMPLATE", "e2b/openai-desktop"
)


def _resolve_api_key() -> str | None:
    """Allow QMCLOUD_API_KEY to alias E2B_API_KEY so users see one name."""
    key = os.getenv("QMCLOUD_API_KEY") or os.getenv("E2B_API_KEY")
    if key:
        os.environ["E2B_API_KEY"] = key
    return key


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--template", default=DEFAULT_DESKTOP_TEMPLATE)
    ap.add_argument("--port", type=int, default=6080)
    ap.add_argument("--timeout", type=int, default=30 * 60)
    args = ap.parse_args()

    if not _resolve_api_key():
        print("QMCLOUD_API_KEY not set (E2B_API_KEY also accepted)", file=sys.stderr)
        return 1

    # New SDK gates public ingress on `network.allow_public_traffic`.
    # Without it the cloud edge returns 502 even though the sandbox is up.
    sbx = Sandbox.create(
        template=args.template,
        timeout=args.timeout,
        network={"allow_public_traffic": True},
    )
    try:
        host = sbx.get_host(args.port)
        url = f"https://{host}"
        print(f"sandbox id : {sbx.sandbox_id}")
        print(f"novnc url  : {url}")
        print(f"timeout    : {args.timeout}s")

        # Readiness probe — noVNC takes a few seconds to start up. Hammer the
        # URL until we see anything other than a 5xx, with a hard cap.
        print("waiting for noVNC to come up...", end="", flush=True)
        deadline = time.time() + 60
        ready = False
        while time.time() < deadline:
            try:
                req = urllib.request.Request(url, method="GET")
                with urllib.request.urlopen(req, timeout=4) as resp:
                    if resp.status < 500:
                        ready = True
                        break
            except urllib.error.HTTPError as e:
                if e.code < 500:
                    ready = True
                    break
            except Exception:
                pass
            print(".", end="", flush=True)
            time.sleep(2)
        print(" ready" if ready else " timed out (try /browserfeed anyway)")
        print()
        print("Paste into qmax-code:  /browserfeed " + url)
        print("Ctrl+C to stop.")
        try:
            while True:
                time.sleep(60)
        except KeyboardInterrupt:
            print("\nshutting down sandbox...")
    finally:
        try:
            sbx.kill()
        except Exception:
            pass
    return 0


if __name__ == "__main__":
    sys.exit(main())
