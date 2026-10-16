{
  description = "Walter: minimal Go chat with llama.cpp and OIDC";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs =
    { self, nixpkgs, ... }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
    in
    {
      packages.${system}.default = pkgs.buildGoModule {
        pname = "walter";
        version = "0.1.0";
        src = pkgs.lib.cleanSource ./.;
        vendorHash = "sha256-sbU1ptUcHexY1CZ7UahJiGtlF4ZL/8xAtbvLN+b/Lqg=";
        env.CGO_ENABLED = "0";
        subPackages = [ "." ];
        ldflags = [
          "-s"
          "-w"
        ];
        checkPhase = ''
          runHook preCheck
          go test ./...
          go vet ./...
          runHook postCheck
        '';
        meta.mainProgram = "walter";
      };

      checks.${system}.walter = self.packages.${system}.default;
      formatter.${system} = pkgs.nixfmt;

      devShells.${system}.default = pkgs.mkShell {
        packages = with pkgs; [
          go
          gopls
          helix
          fish
          sops
          age
          nodejs
          gcc # Required by Go's race detector, not the release binary.
        ];
        GOTOOLCHAIN = "local";
      };
    };
}
