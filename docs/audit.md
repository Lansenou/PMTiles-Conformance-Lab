# Public-release hygiene audit

Date: 2026-09-26. Scope: every tracked file on the integration branch, plus commit metadata.

| Check | Method | Result |
|---|---|---|
| Private names, local paths, internal hosts | `git grep -i` for home and temp paths, session/scratch paths, private domains, e-mail addresses | none in tracked files |
| Credentials | `git grep -i` for password, secret, token, api key, private key headers | none. The only matches are prose saying tests need no secrets, and CI's `persist-credentials: false`. |
| Network endpoints in code | URLs in `*.go`, `*.mjs`, `*.sh`, `*.yml` | only `127.0.0.1`, reserved example domains (`*.example`, `origin.invalid`), and public spec/project links |
| Binary files | every tracked file checked for text | only `fixtures/**/*.pmtiles`, all written by `pmtiles-lab generate` and pinned by `SHA256SUMS` and `TestGolden` |
| Real map data, screenshots, borrowed fixtures | file list review | none; tiles are solid-colour PNGs from constants ([fixtures.md](fixtures.md)) |
| Copied source | written for this repository; upstream repos were read for the spec and API behaviour only | no upstream code or fixtures; the Go module has no dependencies |
| Licenses | `LICENSE` (Apache-2.0), `NOTICE`; `THIRD_PARTY_NOTICES.md` lists tools installed at check time | complete |
| Commit metadata | `git log --format='%an <%ae>'` | all commits authored as `Claude <noreply@anthropic.com>`; trailers carry a claude.ai session link, which the owner may want to review before the repository becomes public |
