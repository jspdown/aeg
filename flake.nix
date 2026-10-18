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
          # The builder tests only subPackages, so checks.test runs the tests of all packages.
          doCheck = false;
        };

        e2eTest = aeg.overrideAttrs {
          name = "aeg-e2e-test";
          buildPhase = ''
            mkdir -p $out/bin
            go test -c -tags e2e -o $out/bin/e2e.test ./e2e
          '';
          installPhase = "true";
        };

        # The e2e tests run AEG and srt in a NixOS VM. Nix runs the VM only on Linux.
        e2e = pkgs.testers.runNixOSTest {
          name = "aeg-e2e";
          nodes.machine = {
            environment.systemPackages = [
              pkgs.curl
              pkgs.sandbox-runtime
            ];
            # The test upstream listens on 127.0.0.1. A name that is not in NO_PROXY makes the clients use AEG.
            networking.hosts."127.0.0.1" = [ "upstream.test" ];
          };
          testScript = ''
            machine.wait_for_unit("multi-user.target")
            machine.succeed("cd /tmp && AEG_BIN=${aeg}/bin/aeg ${e2eTest}/bin/e2e.test -test.v >&2")
          '';
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

          # The e2e tests need a VM, so the e2e tag is not set here.
          test = aeg.overrideAttrs {
            name = "aeg-test";
            # The race detector needs cgo.
            CGO_ENABLED = 1;
            buildPhase = ''
              export HOME=$TMPDIR
              go test -race ./...
            '';
            installPhase = "touch $out";
          };
        }
        // pkgs.lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux { inherit e2e; };

        devShells.default = pkgs.mkShell {
          inputsFrom = [ aeg ];
          packages = [
            pkgs.golangci-lint
            pkgs.govulncheck
            pkgs.lima
            g2n.gomod2nix
          ];
        };
      }
    );
}
