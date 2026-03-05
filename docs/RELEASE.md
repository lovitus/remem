# Release Guide

## Local release

1. Build artifacts and checksums:

```bash
bash ./scripts/release.sh v0.2.0
```

2. Push code and tag:

```bash
git push origin main
git tag v0.2.0
git push origin v0.2.0
```

3. Create GitHub release:

```bash
gh release create v0.2.0 dist/release/* --title "v0.2.0" --notes-file CHANGELOG.md
```

## GitHub Actions release

Pushing a tag `v*` triggers `.github/workflows/release.yml` to build and upload artifacts.
