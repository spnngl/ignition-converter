# ignition-converter

Mutating admission webhook that converts the Ignition user data of Cluster API bootstrap Secrets from spec 2.x to 3.0, so Fedora CoreOS machines boot. `cmd/ignition-converter` is the binary; `README.md` documents its behavior and deployment.

## Rules

- Edit what agents read (every `AGENTS.md`, code comments) with the `writing-for-agents` skill. Each `AGENTS.md` has a `CLAUDE.md` symlink beside it.
- Finish a change when `make check` passes. A change to the conversion also passes `./e2e.sh` (header: requirements).
- Commit with the `git-commit` skill. Pull requests fail CI unless every commit subject is a Conventional Commit (`type(scope)!: description`; types feat, fix, docs, style, refactor, perf, test, build, ci, chore, revert). release-please derives the next version and the changelog from them.
- Keep the fixtures in `cmd/ignition-converter/testdata/` the output of Cluster API's renderer; `convert_test.go` documents where they come from and when to regenerate them.
- The container image is built in `github.com/spnngl/images` and published as `ghcr.io/spnngl/images/ignition-converter`; container changes go there. Its build checks `--version`.

## Read before you work

- Before editing `.github/`, `renovate.json5` or the release-please files, read `.github/AGENTS.md`.
