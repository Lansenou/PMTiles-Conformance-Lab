# Proposal: manually approved release workflow

Not implemented. This proposal is for review once the CLI is considered stable. Nothing in the repository publishes or deploys today.

## Shape

* **Trigger:** `workflow_dispatch` with a `tag` input (for example `v0.2.0`), run by a maintainer. There is no trigger on tag push, so creating a tag alone does nothing.
* **Approval:** the job runs in a GitHub Environment named `release` with required reviewers, so a second person approves before anything is built for publication.
* **Permissions:** `contents: read` for the build job. Only the final upload job gets `contents: write`, and it creates a **draft** release that a maintainer publishes by hand.
* **Steps:**
  1. Check out the tag.
  2. Run `scripts/check.sh`.
  3. Build with `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -buildid="` for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 and windows/amd64.
  4. Write `SHA256SUMS` for the binaries, and attach binaries, sums and a tarball of `fixtures/`.
* **Pinning:** actions pinned by commit SHA as in `ci.yml`. No third-party release actions; use `gh release create --draft` from the preinstalled GitHub CLI.
* **Reproducibility:** `-trimpath` and an empty build ID let anyone rebuild the same bytes with the same Go version. The Go version is taken from `go.mod`.

## Draft

```yaml
name: release
on:
  workflow_dispatch:
    inputs:
      tag: { description: "existing tag to build, e.g. v0.2.0", required: true }
permissions: { contents: read }
jobs:
  build:
    runs-on: ubuntu-latest
    timeout-minutes: 20
    environment: release          # required reviewers configured in repo settings
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with: { ref: "${{ inputs.tag }}", persist-credentials: false }
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with: { go-version-file: go.mod, cache: false }
      - run: ./scripts/check.sh
      - run: |
          mkdir dist
          for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
            os=${t%/*}; arch=${t#*/}; ext=; [ "$os" = windows ] && ext=.exe
            CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags="-s -w -buildid=" \
              -o "dist/pmtiles-lab_${{ inputs.tag }}_${os}_${arch}${ext}" ./cmd/pmtiles-lab
          done
          tar -czf "dist/pmtiles-lab-fixtures_${{ inputs.tag }}.tar.gz" fixtures
          (cd dist && sha256sum * > SHA256SUMS)
      - uses: actions/upload-artifact@<pin-by-sha>   # handed to the draft-release job
        with: { name: dist, path: dist }
  draft-release:
    needs: build
    runs-on: ubuntu-latest
    timeout-minutes: 5
    permissions: { contents: write }
    steps:
      - uses: actions/download-artifact@<pin-by-sha>
        with: { name: dist, path: dist }
      - run: gh release create "${{ inputs.tag }}" dist/* --draft --verify-tag --title "${{ inputs.tag }}"
        env: { GH_TOKEN: "${{ github.token }}", GH_REPO: "${{ github.repository }}" }
```

Open decisions for the maintainer: which platforms to ship, and whether to sign artifacts (for example with GitHub artifact attestations).
