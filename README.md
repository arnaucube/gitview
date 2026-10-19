# Gitview

A self-hosted Git repository browser written in Go, with a GitHub-inspired interface. Browse repositories 10 per page with search and sorting, plus files, READMEs, history, diffs, branches, and tags; clone repositories or download archives. Language indicators and size-weighted breakdowns are computed locally using [go-enry](https://github.com/go-enry/go-enry). Vendor, generated, binary, documentation, and data files are excluded; detection can differ from GitHub and does not apply `.gitattributes` overrides. HTTP push is disabled.

## Run

Requires Go 1.23+ and Git.

```sh
go build -o bin/gitview ./cmd
./bin/gitview --config ./config.yaml
```

Open http://localhost:5555. In [config.yaml](config.yaml), set `repo.scanPath` to your repository directory, `meta.title` to the browser tab title, and `meta.username` to the name shown in the top bar and on repository pages. Repositories must contain at least one commit.

Set `meta.logo` to an image path relative to `dirs.static` (for example `branding/logo.png` for `static/branding/logo.png`). The same image is used as the top-bar logo and favicon:

```yaml
meta:
  title: git.website.com
  username: yourusername
  logo: favicon.svg
```

To show selected repositories first, list them in the desired order:

```yaml
repo:
  pinned: [project-one, project-two]
```

The repository catalog is cached in memory. `repo.refreshInterval` defaults to `30s`: after that interval, a list request triggers one background refresh while serving the previous catalog. The first request after startup builds the catalog; refresh failures retain the last successful list. Language results are reused while the commit stays unchanged.

Names may include the `.git` suffix. Other repositories follow by last updated; ignored or unlisted repositories stay hidden. Restart Gitview after configuration or list-template changes.

Or run with Docker:

```sh
docker compose -f docker/docker-compose.yml up --build
```

## Mirror GitHub repositories

Mirror all public repositories owned by a GitHub user (including forks and archived repositories):

```sh
./scripts/mirror-github.sh githubusername
# Custom directory: use the value of repo.scanPath.
./scripts/mirror-github.sh githubusername /opt/gitview/repos
```

Requires Bash, Git, curl, Python 3, and `flock`. Defaults to this project's `repos/` directory. Rerun to discover new repositories and update existing mirrors, descriptions, and default branches. An optional `GITHUB_TOKEN` environment variable increases the API rate limit.

The script uses `git clone --mirror` and fetches updates with pruning. Upstream updates overwrite mirrored refs; deleted upstream refs are removed. Keep development changes in a separate clone. Unrelated local repositories are skipped with an error; repositories removed from GitHub are kept locally. Gitview displays updates without restarting.

For automatic updates every five minutes, run `crontab -e` as the repository owner and add this line, replacing the paths and username:

```cron
*/5 * * * * /opt/gitview/scripts/mirror-github.sh githubusername /opt/gitview/repos >> /opt/gitview/mirror.log 2>&1
```

The script locks the destination to prevent overlapping updates. The owner must be able to write the repositories and log file. With Docker, run synchronization on the host; Compose mounts the repositories read-only into Gitview.

Mirrors copy Git data, not GitHub issues, pull requests, or release attachments. See the [Git mirror documentation](https://git-scm.com/docs/git-clone#Documentation/git-clone.txt---mirror).

## Checks

```sh
go test ./...
go vet ./...
python3 -B scripts/tests/test_mirror_github.py
```

