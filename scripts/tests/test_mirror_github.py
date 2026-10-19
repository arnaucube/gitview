"""Exercise the mirror script against local Git repositories and a stub GitHub API."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / "mirror-github.sh"


class MirrorGitHubTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.sources = self.root / "sources"
        self.sources.mkdir()
        self.destination = self.root / "served repos"
        self.fixtures = self.root / "pages"
        self.fixtures.mkdir()
        tools = self.root / "tools"
        tools.mkdir()
        # Stub only HTTP discovery; every clone/fetch uses real Git with local URL rewriting.
        curl = tools / "curl"
        curl.write_text('''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
from urllib.parse import parse_qs, urlparse
if os.environ.get("TEST_API_FAILURE"):
    sys.exit(22)
args = sys.argv[1:]
assert not any("test-token" in arg for arg in args)
headers = sys.stdin.read()
if os.environ.get("GITHUB_TOKEN"):
    assert "Authorization: Bearer test-token" in headers
url = urlparse(args[-1])
assert url.path == "/users/testuser/repos"
query = parse_qs(url.query)
assert query["per_page"] == ["100"]
page = query["page"][0]
with open(os.environ["TEST_API_CALLS"], "a") as calls:
    calls.write(page + "\\n")
Path(args[args.index("--output") + 1]).write_bytes(
    (Path(os.environ["TEST_API_PAGES"]) / (page + ".json")).read_bytes())
''')
        curl.chmod(0o755)
        self.env = os.environ.copy()
        # Isolate Git configuration, identities, credentials, and URL rewrites from the host.
        self.env.update({
            "PATH": str(tools) + os.pathsep + self.env["PATH"],
            "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_CONFIG_GLOBAL": "/dev/null",
            "GIT_CONFIG_COUNT": "1",
            "GIT_CONFIG_KEY_0": f"url.{self.sources.as_uri()}/.insteadOf",
            "GIT_CONFIG_VALUE_0": "https://github.com/testuser/",
            "GIT_AUTHOR_NAME": "Mirror Test",
            "GIT_AUTHOR_EMAIL": "mirror@example.com",
            "GIT_COMMITTER_NAME": "Mirror Test",
            "GIT_COMMITTER_EMAIL": "mirror@example.com",
            "TEST_API_PAGES": str(self.fixtures),
            "TEST_API_CALLS": str(self.root / "calls"),
            "GITHUB_TOKEN": "test-token",
        })
        self.env.pop("TEST_API_FAILURE", None)

    def git(self, directory, *args):
        result = subprocess.run(["git", "-C", str(directory), *args], env=self.env,
                                text=True, capture_output=True, check=True)
        return result.stdout.strip()

    def source(self, name, empty=False):
        path = self.sources / (name + ".git")
        path.mkdir()
        self.git(path, "init", "--initial-branch=main")
        if not empty:
            (path / "README.md").write_text("# Example\n")
            self.git(path, "add", "README.md")
            self.git(path, "commit", "-m", "Initial source")
        return path

    def page(self, repos, number=1):
        (self.fixtures / f"{number}.json").write_text(json.dumps(repos))

    def record(self, name, **overrides):
        return {"name": name, "private": False, "default_branch": "main",
                "description": "A description\twith tabs\nand newlines.", **overrides}

    def run_script(self, success=True):
        result = subprocess.run([str(SCRIPT), "testuser", str(self.destination)],
                                env=self.env, text=True, capture_output=True, timeout=30)
        if success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0)
        return result

    def test_clone_refresh_and_prune(self):
        source = self.source("project")
        initial_hash = self.git(source, "rev-parse", "HEAD")
        self.git(source, "branch", "obsolete")
        self.git(source, "tag", "v1")
        self.page([self.record("project", fork=True, archived=True)])
        self.run_script()
        mirror = self.destination / "project.git"
        self.assertEqual(self.git(mirror, "rev-parse", "HEAD"), self.git(source, "rev-parse", "HEAD"))
        self.assertEqual(self.git(mirror, "rev-parse", "--is-bare-repository"), "true")
        self.assertEqual((mirror / "description").read_text(), self.record("project")["description"] + "\n")
        self.git(source, "branch", "-m", "trunk")
        self.git(source, "branch", "-D", "obsolete")
        self.git(source, "tag", "-d", "v1")
        (source / "README.md").write_text("# Changed\n")
        self.git(source, "commit", "-am", "Upstream update")
        self.page([self.record("project", default_branch="trunk", description=None)])
        self.run_script()
        self.assertEqual(self.git(mirror, "symbolic-ref", "HEAD"), "refs/heads/trunk")
        self.assertEqual(self.git(mirror, "rev-parse", "HEAD"), self.git(source, "rev-parse", "HEAD"))
        self.assertEqual(self.git(mirror, "for-each-ref", "--format=%(refname)"), "refs/heads/trunk")
        self.assertEqual((mirror / "description").read_text(), "\n")
        self.git(source, "update-ref", "refs/heads/trunk", initial_hash)
        self.run_script()
        self.assertEqual(self.git(mirror, "rev-parse", "HEAD"), initial_hash)

    def test_pagination_and_public_only_filter(self):
        self.source("last-page")
        # A full first page must not stop pagination even if every entry is filtered out.
        self.page([self.record(f"private-{i}", private=True) for i in range(100)])
        self.page([self.record("last-page")], number=2)
        self.run_script()
        self.assertEqual((self.root / "calls").read_text(), "1\n2\n")
        self.assertEqual(sorted(p.name for p in self.destination.glob("*.git")), ["last-page.git"])

    def test_preserves_unrelated_destination_and_continues(self):
        self.source("project")
        self.source("other")
        existing = self.destination / "project.git"
        existing.mkdir(parents=True)
        (existing / "important.txt").write_text("Keep this file")
        self.page([self.record("project"), self.record("other")])
        result = self.run_script(success=False)
        self.assertIn("not a matching GitHub mirror", result.stderr)
        self.assertEqual((existing / "important.txt").read_text(), "Keep this file")
        self.assertTrue((self.destination / "other.git" / "HEAD").is_file())

    def test_refuses_existing_bare_clones_and_foreign_mirrors(self):
        self.destination.mkdir()
        for name, mode in (("bare", "--bare"), ("foreign", "--mirror")):
            source = self.source(name)
            target = self.destination / (name + ".git")
            self.git(self.root, "clone", mode, str(source), str(target))
        self.page([self.record("bare"), self.record("foreign")])
        self.run_script(success=False)
        for name in ("bare", "foreign"):
            target = self.destination / (name + ".git")
            self.assertEqual(self.git(target, "config", "--get", "remote.origin.url"),
                             str(self.sources / (name + ".git")))
            self.assertEqual(self.git(target, "rev-parse", "HEAD"),
                             self.git(self.sources / (name + ".git"), "rev-parse", "HEAD"))

    def test_refuses_symlink_destination(self):
        source = self.source("project")
        self.destination.mkdir()
        (self.destination / "project.git").symlink_to(source, target_is_directory=True)
        self.page([self.record("project")])
        self.run_script(success=False)
        self.assertFalse((source / "description").exists())

    def test_empty_repository_and_failed_clone(self):
        self.source("empty", empty=True)
        self.source("working")
        self.page([self.record("missing"), self.record("empty"), self.record("working")])
        result = self.run_script(success=False)
        self.assertIn("Mirrored 2 repositories", result.stdout)
        self.assertFalse((self.destination / "missing.git").exists())
        self.assertTrue((self.destination / "working.git" / "HEAD").is_file())
        self.assertEqual([p for p in self.destination.glob(".github-mirror.*") if p.is_dir()], [])

    def test_api_failure_does_not_touch_repositories(self):
        self.env["TEST_API_FAILURE"] = "1"
        result = self.run_script(success=False)
        self.assertIn("GitHub API request failed", result.stderr)
        self.assertEqual(list(self.destination.glob("*.git")), [])
        self.assertEqual([p for p in self.destination.glob(".github-mirror.*") if p.is_dir()], [])

    def test_destination_lock(self):
        self.destination.mkdir()
        self.page([])
        import fcntl
        with (self.destination / ".github-mirror.lock").open("w") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            result = self.run_script(success=False)
            self.assertIn("already running", result.stderr)
        self.assertFalse((self.root / "calls").exists())

    def test_rejects_invalid_username(self):
        result = subprocess.run([str(SCRIPT), "../unsafe", str(self.destination)],
                                env=self.env, text=True, capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.destination.exists())


if __name__ == "__main__":
    unittest.main()
