#!/usr/bin/env python3
"""Verify one exact release before using the Render Free API."""

import argparse
from datetime import datetime, timedelta, timezone
import json
import os
import re
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


REQUIRED_PLANS = {
    "render": "free",
    "neon": "free",
    "upstash": "free",
    "resend": "free",
    "supabase": "free",
    "cloudflare": "workers-free",
    "github-actions": "free",
}
SHA = re.compile(r"[a-f0-9]{40}")
RENDER_ORIGIN = re.compile(r"https://[a-z0-9-]+\.onrender\.com")


class ReleaseBlocked(Exception):
    pass


def require(condition, message):
    if not condition:
        raise ReleaseBlocked(message)


def recent(stamp, now):
    if not isinstance(stamp, str):
        return False
    try:
        moment = datetime.fromisoformat(stamp.replace("Z", "+00:00"))
        return moment.tzinfo is not None and timedelta(0) <= now - moment <= timedelta(hours=24)
    except ValueError:
        return False


def verify_evidence(evidence, sha, now):
    require(SHA.fullmatch(sha) and evidence.get("releaseSha") == sha,
            "release evidence must identify the exact commit SHA")
    qa = evidence.get("qa", {})
    require(qa.get("sha") == sha and recent(qa.get("verifiedAt"), now)
            and qa.get("playwright") == qa.get("playwrightMcp") == "passed",
            "fresh exact-SHA Playwright and interactive QA evidence required")
    accounts = evidence.get("providers", {})
    require(set(REQUIRED_PLANS) <= accounts.keys() <= set(REQUIRED_PLANS) | {"grafana-cloud"},
            "evidence must cover each required provider")
    for provider, record in accounts.items():
        require(record.get("plan") == REQUIRED_PLANS.get(provider, "free")
                and recent(record.get("verifiedAt"), now)
                and record.get("noPaymentMethod") is True
                and record.get("hardQuotaStop") is True
                and record.get("overageBilling") is False
                and isinstance(record.get("source"), str) and bool(record["source"].strip()),
                f"fresh free-tier hard-stop evidence required for {provider}")
    github = accounts["github-actions"]
    require(type(github.get("spendLimitUSD")) is int and github["spendLimitUSD"] == 0
            and github.get("freeMinutesHardStop") is True,
            "GitHub Actions requires a $0 spend limit and free-minute hard stop")
    render = accounts["render"]
    require(render.get("autoDeployOff") is True and isinstance(render.get("serviceId"), str)
            and re.fullmatch(r"srv-[a-z0-9]+", render["serviceId"]),
            "Render evidence must identify a Free service with auto-deploy off")
    upstash = accounts["upstash"]
    require(upstash.get("noEviction") is True and upstash.get("durableWrites") is True,
            "Upstash evidence must verify persistent non-evictable state")
    return render["serviceId"]


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def api_request(origin, path, token, body=None):
    data = json.dumps(body).encode() if body is not None else None
    request = urllib.request.Request(origin + path, data=data, headers={
        "Authorization": "Bearer " + token,
        "Accept": "application/json",
        "Content-Type": "application/json",
        "User-Agent": "social-media-nlp-release",
    })
    try:
        opener = urllib.request.build_opener(NoRedirect())
        with opener.open(request, timeout=20) as response:
            return json.load(response)
    except urllib.error.HTTPError as err:
        raise ReleaseBlocked(f"API returned HTTP {err.code}") from None
    except (urllib.error.URLError, TimeoutError, ValueError):
        raise ReleaseBlocked("API unavailable or response invalid") from None


def verify_ci(repository, sha, token):
    origin = "https://api.github.com"
    query = urllib.parse.urlencode({"head_sha": sha, "status": "success", "per_page": 20})
    result = api_request(origin, f"/repos/{repository}/actions/workflows/ci-security.yml/runs?{query}", token)
    for run in result.get("workflow_runs", []):
        if (run.get("head_sha") != sha or run.get("conclusion") != "success"
                or run.get("event") not in ("push", "workflow_dispatch")):
            continue
        run_id = run.get("id")
        if type(run_id) is not int:
            continue
        jobs = api_request(origin, f"/repos/{repository}/actions/runs/{run_id}/jobs?filter=latest", token)
        if any(job.get("name") == "quality-security" and job.get("conclusion") == "success"
               for job in jobs.get("jobs", [])):
            return
    raise ReleaseBlocked("no passing CI and security job for this exact commit")


def verify_render(repository, service_id, token):
    origin = "https://api.render.com"
    resource = f"/v1/services/{service_id}"
    service = api_request(origin, resource, token)
    settings = service.get("serviceDetails", {})
    auto_deploy = service.get("autoDeployTrigger", "off" if service.get("autoDeploy") == "no" else "")
    allowed_repo = "https://github.com/" + repository
    require(service.get("id") == service_id and service.get("type") == "web_service"
            and service.get("repo", "").removesuffix(".git").lower() == allowed_repo.lower()
            and settings.get("plan") == "free" and auto_deploy == "off",
            "Render API did not confirm the Free repository service and disabled auto-deploy")
    origin_url = settings.get("url", "")
    require(isinstance(origin_url, str) and RENDER_ORIGIN.fullmatch(origin_url),
            "Render service must have a same-origin onrender.com URL")
    return resource, origin_url


def deploy(resource, sha, token):
    origin = "https://api.render.com"
    response = api_request(origin, resource + "/deploys", token, {"commitId": sha})
    deploy_id = response.get("id", "")
    require(isinstance(deploy_id, str) and re.fullmatch(r"dep-[a-z0-9]+", deploy_id),
            "Render did not return a deployment ID")
    deadline = time.monotonic() + 720
    while time.monotonic() < deadline:
        details = api_request(origin, resource + "/deploys/" + deploy_id, token)
        commit = details.get("commit") or {}
        if commit.get("id"):
            require(commit["id"] == sha, "Render prepared a different commit")
        status = details.get("status")
        if status == "live":
            require(commit.get("id") == sha, "Render did not confirm the live commit")
            return
        require(status in {"created", "queued", "build_in_progress", "update_in_progress",
                           "pre_deploy_in_progress"}, "Render deployment did not succeed")
        time.sleep(12)
    raise ReleaseBlocked("Render deployment did not reach live status in time")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=["evidence", "preflight", "deploy"])
    parser.add_argument("--sha", required=True)
    options = parser.parse_args()
    try:
        evidence = json.loads(os.environ.get("NOCARD_RELEASE_EVIDENCE", ""))
        service_id = verify_evidence(evidence, options.sha, datetime.now(timezone.utc))
        if options.mode == "evidence":
            print("Exact-SHA evidence validated")
            return 0
        repository = os.environ.get("GITHUB_REPOSITORY", "")
        github_token = os.environ.get("GH_TOKEN", "")
        render_token = os.environ.get("RENDER_API_KEY", "")
        require(re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository)
                and os.environ.get("GITHUB_SHA") == options.sha and github_token and render_token,
                "workflow ref, repository and release credentials required")
        verify_ci(repository, options.sha, github_token)
        resource, url = verify_render(repository, service_id, render_token)
        if options.mode == "deploy":
            output = os.environ.get("GITHUB_OUTPUT")
            require(output, "GitHub workflow output is required")
            subprocess.run(["go", "run", "./cmd/release-cache"], cwd="backend", check=True)
            verify_evidence(evidence, options.sha, datetime.now(timezone.utc))
            deploy(resource, options.sha, render_token)
            with open(output, "a", encoding="utf-8") as sink:
                sink.write(f"base_url={url}\n")
        print("Release checks passed")
        return 0
    except ReleaseBlocked as err:
        print(f"Release blocked: {err}", file=sys.stderr)
    except (AttributeError, KeyError, TypeError, ValueError):
        print("Release blocked: missing or malformed evidence/API response", file=sys.stderr)
    except (OSError, subprocess.CalledProcessError):
        print("Release blocked: Upstash TLS capability check or output failed", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
