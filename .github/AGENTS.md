# Workflows and Renovate

The shared workflows and the Renovate preset come from `github.com/spnngl/.github`; its `README.md` documents their inputs and permissions.

- Pin third-party actions to a full commit SHA with a `# vX.Y.Z` comment. `spnngl/*` references stay at `@main`: the preset disables their updates and `zizmor.yml` allows them.
- Annotate a tool version with `# renovate: datasource=<datasource> depName=<name>` on the line right above `key: "<version>"`, unless Renovate already tracks it (its github-actions manager reads `golangci-lint-action`'s `version`).
- `release.yml` runs release-please (`release-please-config.json`, `.release-please-manifest.json`) on every push to `main`. Merging its pull request creates the tag and a draft release; the same run builds, checks and publishes, because a tag pushed with `GITHUB_TOKEN` triggers no workflow. The assets come from `make dist`, which CI also runs. When a job after `release-please` fails, fix the cause and use "Re-run failed jobs": a full re-run finds the release pull request already tagged and releases nothing, leaving the draft unpublished.
- Grant permissions per job, with `permissions: {}` at the top.
- Done when `actionlint` and `zizmor .` pass, and `renovate-config-validator --strict renovate.json5` for a Renovate change.
