{
  description = "SPAR bot container build tooling and NixOS service";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  outputs = { self, nixpkgs }: let
    systems = [ "x86_64-linux" "aarch64-linux" ];
    each = nixpkgs.lib.genAttrs systems;
  in {
    # This intentionally uses the existing Dockerfile, not a native Go/Paddle derivation.
    # Networked image builds are not sandboxed/reproducible Nix derivations.
    apps = each (system: let
      pkgs = nixpkgs.legacyPackages.${system};
      src = pkgs.lib.fileset.toSource {
        root = ./.;
        fileset = pkgs.lib.fileset.unions [ ./Dockerfile ./go.mod ./go.sum ./pyproject.toml ./uv.lock ./.python-version ./cmd ./internal ];
      };
      build = pkgs.writeShellApplication {
        name = "build-spar-image";
        runtimeInputs = [ pkgs.podman ];
        text = ''
          exec podman build -t "''${1:-localhost/spar-tilbuds-bot:migration}" ${src}
        '';
      };
    in { default = { type = "app"; program = "${build}/bin/build-spar-image"; }; });
    nixosModules.default = { config, lib, ... }: let cfg = config.services.sparBot; in {
      options.services.sparBot = {
        enable = lib.mkEnableOption "containerized SPAR bot";
        image = lib.mkOption { type = lib.types.str; description = "Published image digest or preloaded rootful Podman image"; };
        environmentFile = lib.mkOption { type = lib.types.str; default = "/var/lib/secrets/spar.env"; };
        dataDir = lib.mkOption { type = lib.types.str; default = "/srv/spar"; };
      };
      config = lib.mkIf cfg.enable {
        virtualisation.podman.enable = true;
        virtualisation.oci-containers.backend = "podman";
        systemd.tmpfiles.rules = [ "d ${cfg.dataDir} 0700 root root -" ];
        virtualisation.oci-containers.containers.spar-bot = {
          image = cfg.image;
          environmentFiles = [ cfg.environmentFile ];
          environment = {
            # Matches the image defaults (see Dockerfile): unprivileged HOME for
            # the PaddleOCR model cache, state mount and timezone.
            HOME = "/home/bot";
            PADDLEOCR_PYTHON = "/app/.venv/bin/python";
            PYTHONIOENCODING = "utf-8";
            PYTHONUTF8 = "1";
            CONFIG_PATH = "/data/config.toml";
            TZ = "Europe/Copenhagen";
          };
          volumes = [ "${cfg.dataDir}:/data" "spar-work:/app/work" "spar-paddle-cache:/home/bot" ];
          extraOptions = [ "--init" "--read-only" "--security-opt=no-new-privileges" "--cap-drop=ALL" "--tmpfs=/tmp" ];
        };
      };
    };
  };
}
