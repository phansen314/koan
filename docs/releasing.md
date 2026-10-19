# Releasing

The plugin serves the skill from the release tag, so the skill and `go install …@latest` move together.

1. CI is green on `main`.
2. Pick the version: before 1.0, a new command or any change to the formats or the JSON output bumps the minor version; anything else, the patch.
3. Set `ref` in [`.claude-plugin/marketplace.json`](../.claude-plugin/marketplace.json) to the new tag (until the first release the plugin's `source` is `"./"`: replace it with `{ "source": "github", "repo": "phansen314/koan", "ref": "vX.Y.Z" }`), and the skill's minimum version if the skill now needs this release; commit.
4. Tag and push together: `git tag -a vX.Y.Z -m "…"` then `git push --atomic origin main vX.Y.Z`.
5. `gh release create vX.Y.Z` with notes on what changed and how to fix existing trees.
6. Check: `GOBIN=$(mktemp -d) GOPROXY=https://proxy.golang.org go install github.com/phansen314/koan/cmd/koan@vX.Y.Z`, and that binary's `koan version` reports `X.Y.Z`.
