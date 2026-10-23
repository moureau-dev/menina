# Menina — AI Development Guide

## Critical rules

- **Do NOT commit or push any changes without explicit user consent.**
- **Do NOT cut a release, tag, or create a GitHub release without explicit consent.**
- **NEVER choose a release version yourself.** Ask the user for the exact version
  and wait for explicit confirmation before editing `CHANGELOG.md`, tagging, or
  creating a release.
- **NEVER discard working-tree changes.** Do not use `git checkout --`,
  `git restore`, `git reset --hard`, `git clean`, or similar. If you need to
  retest an older state, roll back, or revert temporarily, stash the specific
  files instead:
  `git stash push -m "<reason>" -- <paths>`, then restore with `git stash pop`.
- Stage specific files by path. Never use `git add -A` or `git add .`.
- Never print or commit secret values, registry credentials, or session IDs.
- Do not add third-party dependencies without explicit permission.

## Go doc style

Keep doc comments clean, concise, plain English. No em dashes, no developer
anecdotes, no bug-history explanations. Describe what a package, type, or
function is and does.

- Every exported package/type/function should have a doc comment when the name or
  behavior is not obvious.
- Package comments belong in the package's primary file.
- Keep comments close to the code they describe.

## Documentation

After any significant change, check these files need updating:

- `README.md` — public overview, install/quickstart, docs table.
- `CHANGELOG.md` — add to `[Unreleased]` under Added/Fixed/Changed/Docs.
- `ROADMAP.md` — move landed work out of the roadmap, update tiers/dependency
  map when priorities, scope, or item dependencies change.
- `llms.txt` — role-first AI contract: client vs host, commands/flags, safety
  rules, limits, and doc links. Keep links absolute.
- `docs/*.md` — CLI, configuration, proxy, registry, operations, client CLI.
- `docs/STRUCTURE.md` — update when adding, removing, or moving packages,
  top-level directories, or major files.
- `examples/.menina.example.yml`, `internal/contract/schema/menina.schema.json`, and the
  `menina init` template when manifest fields change. Keep parser, schema,
  example, and contract tests in sync.
- `scripts/install*.sh` / `.ps1` when install paths, asset names, or PATH
  behavior change.
- `website/` copy/links when public messaging or docs structure changes.

Docs-only changes are excluded from CI for `.md`, `docs/`, `llms.txt`,
`website/`, `LICENSE`, `.gitignore`, `.dockerignore`, and `examples/`. Run Go
tests locally if code or schema behavior changed.

### Website deploy

Changes to `docs/`, `llms.txt`, `internal/contract/schema/menina.schema.json`, or the
installer scripts are served by the website. After changing them:

```bash
cd website && bun run build
```

Then deploy `website/dist/ssg` to `menina.moureau.dev` with [Basebox](https://basebox.moureau.dev/llms.txt).
The sync step copies docs, rewrites links to absolute URLs, and publishes the schema and installers.

## Project

- Go 1.26, Cobra CLI, YAML via `gopkg.in/yaml.v3`.
- The host binary is Linux-only. The standalone client must stay cross-platform
  (Linux, macOS, Windows). Keep host-only packages out of `internal/cli/client`,
  `internal/contract/manifest`, and `internal/contract/schema`.
- Desired state is YAML under `/etc/menina`; operational state is SQLite.
- The manifest is a front-end to the same config model. Do not invent runtime
  semantics that the resolver or reconciler do not understand.
- `source.repo` is an ownership hint, not authorization.
- No secrets in manifests, examples, logs, or `--json` output.

Commands:

```bash
go test ./...
go test -race ./...
go vet ./...
gofmt -w <paths>
go build ./...
make build          # host binary
make client-all     # client binaries for Linux/macOS/Windows
```

Schema changes must keep the reflection contract test passing and the versioned
schema URL accurate.

## File structure

See [`docs/STRUCTURE.md`](docs/STRUCTURE.md) for the repository tree and package
responsibilities. Update that document when the structure changes.

## Commit messages

Match the existing history: imperative mood, sentence case, no period at the
end. Do not use Conventional Commits prefixes such as `feat:` or `fix:`; older
commits contain them, but current practice does not. A scoped subject such as
`Website: ...` is acceptable when it adds useful context.

Keep the subject concise. Add a body only when the change is non-obvious:
explain what changed and why, wrap at roughly 72 characters, and leave a blank
line after the subject. Use short paragraphs, or a bullet list when the change
has several independent parts. Use backticks for commands, flags, and field
names. Avoid filler such as “This commit…” or “I changed…”. Release commits are
`Release v<version>` and normally have no body.

```text
Add implementation dependency map to roadmap
Document per-item prerequisites, difficulty/importance, and the hidden
one-off job prerequisite. Clarify current service-to-service boundaries
in llms.txt and remove the stale Tier 4 placement.

Add classified CLI errors and apply plan/verify
Introduce `clierr` with stable error codes and exit codes (1-7).
With `--json`, failures emit a structured error envelope to stderr.

- Add `apply --diff`/`--dry-run` previews
- Add `apply --verify` read-back checks
- Add `capabilities --host`

Fix bare image names resolving as bundled registry images
Rewrite llms.txt with role-first client and host guidance
Website: add client download links for Linux, macOS, and Windows
Release v0.0.14
```

## Release

Never do this without explicit user consent.

First ask for the exact version and wait for confirmation. Do not infer the bump
from the changes or choose major/minor/patch yourself.

Menina has no version file; the version comes from the git tag and is injected
with `-ldflags`.

1. Move `[Unreleased]` changes to `## [<version>] - <date>` in `CHANGELOG.md` and
   leave a fresh `[Unreleased]`.
2. Commit the bump: `git commit -m "Release v<version>"`.
3. Tag: `git tag -a v<version> -m "Menina v<version>"`.
4. Push: `git push origin main` and `git push origin v<version>`.
5. Create the release:
   `gh release create v<version> --title "Menina v<version>" --notes-file <notes> --latest`.
6. Watch the release workflow:
   `gh run list --workflow=release.yml --limit 1`, then
   `gh run watch <run-id> --exit-status`.
7. If docs or `llms.txt` changed, rebuild and deploy the website.

The release workflow publishes Linux host assets and client assets for
Linux/macOS/Windows on amd64/arm64, plus `checksums.txt`.
