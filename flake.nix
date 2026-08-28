{
  description = "scruff — the worktree-lifecycle substrate for coding agents";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "aarch64-darwin"
        "x86_64-darwin"
        "aarch64-linux"
        "x86_64-linux"
      ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
      version = builtins.replaceStrings [ "\n" ] [ "" ] (builtins.readFile ./VERSION);
    in
    {
      packages = forAll (pkgs: {
        default = pkgs.buildGoModule {
          pname = "scruff";
          inherit version;
          src = ./.;
          # scruff picked up its first dependency (fsnotify, for `scruff watch`) —
          # see go.mod.
          vendorHash = "sha256-gTscipNyZtkaGkzOsEvREFtetqnSwG9HMbRUYbugkHw=";
          ldflags = [ "-X github.com/hausfold/scruff/internal/commands.Version=${version}" ];

          # Build ONLY the CLI. Left unset, buildGoModule walks every directory
          # holding .go files and builds each as `./dir` of the main module —
          # which since the Go SDK landed (#18) includes `sdk/go`, and that is
          # its OWN module (`sdk/go/go.mod`, so consumers can
          # `go get github.com/hausfold/scruff/sdk/go` without inheriting scruff's
          # deps). Go rightly refuses it:
          #   main module (github.com/hausfold/scruff) does not contain package
          #   github.com/hausfold/scruff/sdk/go
          # internal/* still gets compiled — as dependencies of the CLI, which
          # is the only thing this derivation installs.
          subPackages = [ "cmd/scruff" ];

          # The suite is black-box: it drives the built binary with shim gh/lsof
          # on PATH. That is what makes it portable across implementations, and
          # it is why it can run here rather than only in CI.
          nativeCheckInputs = [
            pkgs.bats
            pkgs.git
          ];
          checkPhase = ''
            runHook preCheck
            go build -o scruff ./cmd/scruff
            bats test/scruff.bats
            runHook postCheck
          '';

          meta = {
            description = "Worktree lifecycle for parallel coding agents: park, resume, PR-verified reap";
            license = pkgs.lib.licenses.mit;
            mainProgram = "scruff";
          };
        };

        # The agent skill (ai/SKILL.md), so a consumer can install it without
        # installing scruff at all — no Go toolchain, no binary. (It does NOT
        # isolate the binary's hash: `src = ./.` is unfiltered, so `ai/` is in
        # the Go derivation's closure and a prose edit rebuilds it regardless.)
        # See nix/skill.nix.
        scruff-skill = pkgs.callPackage ./nix/skill.nix { };
      });

      # The family convention: every haus flake exports an overlay so a
      # consumer writes `pkgs.scruff` rather than reaching into
      # `inputs.scruff.packages.${system}`. Same shape as pounce/trill/perch.
      # `final.system` is a deprecated nixpkgs alias — reading it makes every
      # downstream eval print "'system' has been renamed to/replaced by
      # 'stdenv.hostPlatform.system'". Use the real attribute.
      overlays.default = final: _prev: {
        scruff = self.packages.${final.stdenv.hostPlatform.system}.default;
        scruff-skill = self.packages.${final.stdenv.hostPlatform.system}.scruff-skill;
      };

      devShells = forAll (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            gotools
            bats
            git
            gh
          ];
        };
      });
    };
}
