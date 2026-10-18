{
  description = "Agent Egress Gateway";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
    gomod2nix = {
      url = "github:nix-community/gomod2nix";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.flake-utils.follows = "flake-utils";
    };
  };

  outputs =
    {
      nixpkgs,
      flake-utils,
      gomod2nix,
      ...
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        g2n = gomod2nix.legacyPackages.${system};

        aeg = g2n.buildGoApplication {
          pname = "aeg";
          version = "0.1.0";
          src = ./.;
          modules = ./gomod2nix.toml;
          go = pkgs.go;
          subPackages = [ "cmd/aeg" ];
        };
      in
      {
        packages.default = aeg;

        checks = {
          build = aeg;

          lint = aeg.overrideAttrs (old: {
            name = "aeg-lint";
            nativeBuildInputs = old.nativeBuildInputs ++ [ pkgs.golangci-lint ];
            buildPhase = ''
              export HOME=$TMPDIR
              golangci-lint run ./...
            '';
            doCheck = false;
            installPhase = "touch $out";
          });
        };

        devShells.default = pkgs.mkShell {
          inputsFrom = [ aeg ];
          packages = [
            pkgs.golangci-lint
            pkgs.lima
            g2n.gomod2nix
          ];
        };
      }
    );
}
