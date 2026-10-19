#!/usr/bin/env bash
# Create or refresh read-only mirrors of a GitHub user's public repositories.
set -euo pipefail

usage() {
    printf 'Usage: %s USERNAME [DIRECTORY]\n' "${0##*/}"
    printf 'Defaults to this project\047s repos/ directory; use repo.scanPath for a custom location.\n'
    printf 'Requires git, curl, python3, and flock. GITHUB_TOKEN is optional for API rate limits.\n'
}

if [[ ${1:-} == --help || ${1:-} == -h ]]; then
    usage
    exit 0
fi
if (( $# < 1 || $# > 2 )); then
    usage >&2
    exit 1
fi
username=$1
if [[ ! $username =~ ^[a-zA-Z0-9][a-zA-Z0-9-]{0,38}$ ]]; then
    printf 'Invalid GitHub username.\n' >&2
    exit 1
fi
for dependency in git curl python3 flock; do
    if ! command -v "$dependency" >/dev/null 2>&1; then
        printf 'Missing dependency: %s\n' "$dependency" >&2
        exit 1
    fi
done

project_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
destination=${2:-"$project_dir/repos"}
mkdir -p -- "$destination"
destination=$(cd -- "$destination" && pwd)
# Lock the destination so scheduled runs cannot modify the same mirrors concurrently.
exec 9>"$destination/.github-mirror.lock"
if ! flock -n 9; then
    printf 'Another mirror job is already running for %s.\n' "$destination" >&2
    exit 1
fi
staging=$(mktemp -d "$destination/.github-mirror.XXXXXX")
trap 'rm -rf -- "$staging"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
export GIT_TERMINAL_PROMPT=0

# Send optional credentials over stdin, never in curl's command-line arguments.
fetch_page() {
    local url=$1
    {
        if [[ -n ${GITHUB_TOKEN:-} ]]; then
            printf 'Authorization: Bearer %s\n' "$GITHUB_TOKEN"
        fi
    } | curl --fail --silent --show-error --retry 3 --connect-timeout 15 --max-time 120 \
        --header @- --header 'Accept: application/vnd.github+json' \
        --header 'X-GitHub-Api-Version: 2026-03-10' \
        --output "$staging/page.json" "$url"
}

# Only update matching mirrors; never convert or replace an unrelated local repository.
mirror_repo() {
    local name=$1 branch=$2 description=$3
    local url="https://github.com/$username/$name.git"
    local target="$destination/$name.git"
    if [[ -e $target || -L $target ]]; then
        if [[ -L $target || ! -d $target ]] ||
            [[ $(git -C "$target" rev-parse --is-bare-repository 2>/dev/null) != true ]] ||
            [[ $(git -C "$target" config --get remote.origin.mirror) != true ]] ||
            [[ $(git -C "$target" config --get-all remote.origin.fetch) != '+refs/*:refs/*' ]] ||
            [[ $(git -C "$target" config --get remote.origin.url) != "$url" ]]; then
            printf 'Skipping %s: destination is not a matching GitHub mirror.\n' "$name" >&2
            return 1
        fi
        printf 'Updating %s/%s\n' "$username" "$name"
        git -C "$target" fetch --prune origin || return 1
    else
        printf 'Cloning %s/%s\n' "$username" "$name"
        git clone --mirror -- "$url" "$staging/clone" || return 1
        mv -T -- "$staging/clone" "$target" || return 1
    fi
    # Fetch does not update symbolic HEAD when GitHub changes the default branch.
    if [[ -n $branch ]] && git -C "$target" show-ref --verify --quiet "refs/heads/$branch"; then
        git -C "$target" symbolic-ref HEAD "refs/heads/$branch" || return 1
    fi
    printf '%s\n' "$description" > "$target/description" || return 1
}

page=1
mirrored=0
failed=0
while :; do
    if ! fetch_page "https://api.github.com/users/$username/repos?type=owner&sort=full_name&direction=asc&per_page=100&page=$page"; then
        printf 'GitHub API request failed; check the username, network, and API rate limit.\n' >&2
        exit 1
    fi
    # NUL-separated fields preserve spaces, tabs, and multiline descriptions safely.
    python3 - "$staging/page.json" "$staging/records" "$staging/count" <<'PY'
import json
import re
import sys
from pathlib import Path

repos = json.loads(Path(sys.argv[1]).read_text())
if not isinstance(repos, list):
    raise SystemExit("Expected a repository list from GitHub.")
with open(sys.argv[2], "wb") as records:
    for repo in repos:
        if repo.get("private") is not False:
            continue
        name = repo["name"]
        if not re.fullmatch(r"[A-Za-z0-9_.-]+", name) or name in (".", ".."):
            raise SystemExit("Invalid repository name from GitHub.")
        for value in (name, repo.get("default_branch") or "", repo.get("description") or ""):
            records.write(value.replace("\0", "").encode() + b"\0")
Path(sys.argv[3]).write_text(str(len(repos)))
PY
    while IFS= read -r -d '' name && IFS= read -r -d '' branch && IFS= read -r -d '' description; do
        if mirror_repo "$name" "$branch" "$description"; then
            mirrored=$((mirrored + 1))
        else
            printf 'Failed to mirror %s/%s; continuing.\n' "$username" "$name" >&2
            # Remove only this job's incomplete clone before processing the next repository.
            rm -rf -- "$staging/clone"
            failed=$((failed + 1))
        fi
    done < "$staging/records"
    count=$(cat "$staging/count")
    if (( count < 100 )); then
        break
    fi
    page=$((page + 1))
done
printf 'Mirrored %d repositories to %s; %d failed.\n' "$mirrored" "$destination" "$failed"
(( failed == 0 ))
