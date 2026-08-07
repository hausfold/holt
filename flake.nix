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
          # holt picked up its first dependency (fsnotify, for `holt watch`) —
          # see go.mod.
          vendorHash = "sha256-SAZpfeTKHC/OEgMUWScXYwx7RY6LrSHkHXLg4vArX+g=";
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

      # The family convention: every nebelhaus flake exports an overlay so a
      # consumer writes `pkgs.holt` rather than reaching into
      # `inputs.holt.packages.${system}`. Same shape as pounce/trill/perch.
      # `final.system` is a deprecated nixpkgs alias — reading it makes every
      # downstream eval print "'system' has been renamed to/replaced by
      # 'stdenv.hostPlatform.system'". Use the real attribute.
      overlays.default = final: _prev: {
        holt = self.packages.${final.stdenv.hostPlatform.system}.default;
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
