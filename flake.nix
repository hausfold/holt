{
  description = "holt — the worktree-lifecycle substrate for coding agents";

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
          pname = "holt";
          inherit version;
          src = ./.;
          # holt is dependency-free through 0.1 — see go.mod. When that stops
          # being true this becomes a real hash.
          vendorHash = null;
          ldflags = [ "-X github.com/nebelhaus/holt/internal/commands.Version=${version}" ];

          # The suite is black-box: it drives the built binary with shim gh/lsof
          # on PATH. That is what makes it portable across implementations, and
          # it is why it can run here rather than only in CI.
          nativeCheckInputs = [
            pkgs.bats
            pkgs.git
          ];
          checkPhase = ''
            runHook preCheck
            go build -o holt ./cmd/holt
            bats test/holt.bats
            runHook postCheck
          '';

          meta = {
            description = "Worktree lifecycle for parallel coding agents: park, resume, PR-verified reap";
            license = pkgs.lib.licenses.asl20;
            mainProgram = "holt";
          };
        };
      });

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
