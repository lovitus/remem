# Release Guide

## Local release steps

1. Build archives and checksums:

```bash
bash ./scripts/release.sh v0.1.0
```

2. Create git tag and push:

```bash
git tag v0.1.0
git push origin main --tags
```

3. Create GitHub release:

```bash
gh release create v0.1.0 dist/release/* --title "v0.1.0" --notes-file CHANGELOG.md
```

## GitHub Actions release

Pushing a tag `v*` triggers `.github/workflows/release.yml` to build and upload assets.
