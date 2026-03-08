# Release Guide

## Local release

1. Build artifacts and checksums:

```bash
bash ./scripts/release.sh v0.3.5
```

2. Push code and tag:

```bash
git push origin main
git tag v0.3.5
git push origin v0.3.5
```

3. Create GitHub release:

```bash
gh release create v0.3.5 dist/release/* --title "v0.3.5" --notes-file CHANGELOG.md
```

## GitHub Actions release

Pushing a tag `v*` triggers `.github/workflows/release.yml` to build and upload artifacts.
