# Nix integration

`flake.nix` provides two optional conveniences and nothing else:

- `nix run . -- registry.example/spar-bot:VERSION` builds the repository's
  Dockerfile with Podman.
- `nixosModules.default` runs the published image as a read-only OCI container.

This is a **networked container build**, not a reproducible `nix build` package:
the Dockerfile pulls base images and Python wheels from their registries. Run it
from a clean Git checkout — never from a directory where `.env`, cookies or a
live `config.toml`/`bot.db` could be picked up as build context. Everything that
matters is gitignored, and `flake.lock` pins nixpkgs.

## Building the image

```sh
nix run . -- registry.example/spar-bot:2026-week-42
```

The flake copies only the Dockerfile, the Go/Python manifests and the source
directories into the build context. The Dockerfile pins its Go toolchain stage;
review upstream tag bumps in the usual way before publishing an image.
CI (`.github/workflows/ci.yml`) builds the same Dockerfile on every push, but no
OCR end-to-end run is part of that job.

Push the image and reference its **digest** in the server configuration. If you
load an image locally, remember that the NixOS OCI service uses rootful Podman,
not your user's image store.

## NixOS module

Import `inputs.spar.nixosModules.default` and configure:

```nix
services.sparBot = {
  enable = true;
  image = "registry.example/spar-bot@sha256:REPLACE";
  environmentFile = "/var/lib/secrets/spar.env";   # DISCORD_BOT_TOKEN, DISCORD_GUILD_ID
  dataDir = "/srv/spar";
};
```

The module keeps the container's hardening (read-only rootfs, dropped
capabilities, `init`, `no-new-privileges`, tmpfs `/tmp`), sets
`CONFIG_PATH=/data/config.toml`, `TZ=Europe/Copenhagen` and
`HOME=/home/bot`, and mounts two named volumes for the catalog cache and the
PaddleOCR model cache. No ports are exposed.

The image runs as UID/GID `10001`, so `dataDir` must be writable by that user:

```sh
sudo install -d -o 10001 -g 10001 -m 0700 /srv/spar
```

Place `config.toml` and `bot.db` (if you have existing state) in `dataDir`;
secrets stay in `environmentFile`, outside Nix and Git. Back up `dataDir` before
changing configuration or image versions — the bot keeps price history there.
