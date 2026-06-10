# Contributing

Build and release notes for working on gotempo. For installing and using a release, see the [README](README.md).

## Build from source

```sh
git clone https://github.com/httpsterio/gotempo
cd gotempo
go build -o gotempo .
```

Requires Go 1.24+. Tray icons are embedded at build time. BlueZ is a runtime dependency.

The build stamps a version into the binary (shown in the tray menu and by `gotempo --version`). A plain `go build` reports `dev`; build through `make` for the git-derived version.

For desktop integration from a source build (app-menu entry and icon under `~/.local`, no sudo):

```sh
make install     # make uninstall to remove
```

Override the location with `PREFIX`, e.g. `sudo make install PREFIX=/usr/local`.

## Releasing

The version comes from git tags. The tag you push is the version that ships.

Local builds pick it up from `git describe`:

```sh
make install        # builds with the embedded version
gotempo --version   # e.g. "gotempo v1.0.1"
```

| Situation | Reported version |
|---|---|
| On a tagged commit | `v1.0.1` |
| Past the last tag | `v1.0.1-3-gabc123` |
| Uncommitted changes | `…-dirty` appended |
| No tags yet | `dev` |

Cutting a release is one command:

```sh
make release VERSION=v1.0.2
```

It validates the version, checks the tree is clean, runs the tests, then tags and pushes. Pushing a `v*.*.*` tag triggers `.github/workflows/release.yml`, which builds the binary, packages the `linux-amd64` tarball with the installer, and publishes a GitHub release.
