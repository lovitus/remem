# Release Guide

## Local release

1. Build artifacts and checksums:

```bash
bash ./scripts/release.sh v0.3.7
```

2. Push code and tag:

```bash
git push origin main
git tag v0.3.7
git push origin v0.3.7
```

3. Create GitHub release:

```bash
gh release create v0.3.7 dist/release/* --title "v0.3.7" --notes-file CHANGELOG.md
```

## GitHub Actions release

Pushing a tag `v*` triggers `.github/workflows/release.yml` to build and upload artifacts.
