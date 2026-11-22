import copy
from datetime import datetime, timedelta, timezone
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("release_free", Path(__file__).with_name("release-free.py"))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


class ReleaseGateTests(unittest.TestCase):
    def setUp(self):
        self.sha = "a" * 40
        self.now = datetime(2026, 11, 22, 3, 0, tzinfo=timezone.utc)
        verified = self.now.isoformat()
        providers = {
            key: {"plan": value, "verifiedAt": verified, "noPaymentMethod": True,
                  "hardQuotaStop": True, "overageBilling": False, "source": "offline test fixture"}
            for key, value in release.REQUIRED_PLANS.items()
        }
        providers["github-actions"].update(spendLimitUSD=0, freeMinutesHardStop=True)
        providers["upstash"].update(noEviction=True, durableWrites=True)
        providers["render"].update(autoDeployOff=True, serviceId="srv-test")
        self.evidence = {
            "releaseSha": self.sha,
            "qa": {"sha": self.sha, "verifiedAt": verified, "playwright": "passed", "playwrightMcp": "passed"},
            "providers": providers,
        }

    def test_mandatory_evidence(self):
        self.assertEqual(release.verify_evidence(self.evidence, self.sha, self.now), "srv-test")
        for provider, field, value in [
            ("render", "autoDeployOff", False),
            ("github-actions", "spendLimitUSD", 1),
            ("upstash", "noEviction", False),
            ("cloudflare", "noPaymentMethod", False),
            ("resend", "verifiedAt", (self.now - timedelta(hours=25)).isoformat()),
        ]:
            with self.subTest(provider=provider, field=field):
                invalid = copy.deepcopy(self.evidence)
                invalid["providers"][provider][field] = value
                with self.assertRaises(release.ReleaseBlocked):
                    release.verify_evidence(invalid, self.sha, self.now)
        self.evidence["qa"]["sha"] = "b" * 40
        with self.assertRaises(release.ReleaseBlocked):
            release.verify_evidence(self.evidence, self.sha, self.now)

    def test_skipped_ci_cannot_release(self):
        run = {"head_sha": self.sha, "conclusion": "success", "event": "push", "id": 10}
        for conclusion in ("skipped", "success"):
            with self.subTest(conclusion=conclusion):
                with patch.object(release, "api_request", side_effect=[
                    {"workflow_runs": [run]}, {"jobs": [{"name": "quality-security", "conclusion": conclusion}]},
                ]) as request:
                    if conclusion == "skipped":
                        with self.assertRaises(release.ReleaseBlocked):
                            release.verify_ci("org/repo", self.sha, "fixture-token")
                    else:
                        release.verify_ci("org/repo", self.sha, "fixture-token")
                    self.assertIn("head_sha=" + self.sha, request.call_args_list[0].args[1])

    def test_render_requires_free_pinned_deploy(self):
        service = {"id": "srv-test", "type": "web_service", "repo": "https://github.com/org/repo",
                   "autoDeployTrigger": "off",
                   "serviceDetails": {"plan": "free", "url": "https://demo-test.onrender.com"}}
        with patch.object(release, "api_request", return_value=service):
            resource, _ = release.verify_render("org/repo", "srv-test", "fixture-token")
            service["autoDeployTrigger"] = "commit"
            with self.assertRaises(release.ReleaseBlocked):
                release.verify_render("org/repo", "srv-test", "fixture-token")
        with patch.object(release, "api_request", side_effect=[
            {"id": "dep-test"}, {"status": "live", "commit": {"id": self.sha}},
        ]) as request:
            release.deploy(resource, self.sha, "fixture-token")
            self.assertEqual(request.call_args_list[0].args[-1], {"commitId": self.sha})


if __name__ == "__main__":
    unittest.main()
