{
  description = "tasks — a concurrent hierarchical task runner";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];

      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f system);
    in
    {
      packages = forAllSystems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};

          tasks = pkgs.buildGoModule {
            pname = "tasks";
            version = "0.1.0";

            # Only the files the Go build needs.
            src = pkgs.lib.fileset.toSource {
              root = ./.;
              fileset = pkgs.lib.fileset.unions [
                ./go.mod
                ./go.sum
                ./cmd
                ./internal
              ];
            };

            # Matches the `go` directive in go.mod (go 1.26).
            go = pkgs.go_1_26;
            subPackages = [ "cmd/tasks" ];

            vendorHash = "sha256-OxziBKV+xBN28PgkGueb+pb7cwYrUrA5w/lOocTu9Ls=";

            meta = {
              description = "A concurrent hierarchical task runner";
              homepage = "https://github.com/tomtomsen/tasks";
              mainProgram = "tasks";
              platforms = pkgs.lib.platforms.linux ++ pkgs.lib.platforms.darwin;
            };
          };
        in
        {
          inherit tasks;
          default = tasks;
        });

      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/tasks";
          meta.description = "Run tasks";
        };
        tasks = {
          type = "app";
          program = "${self.packages.${system}.tasks}/bin/tasks";
          meta.description = "Run tasks";
        };
      });

      devShells = forAllSystems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        {
          default = pkgs.mkShell {
            packages = [
              pkgs.go_1_26
              pkgs.gopls
              pkgs.gotools
            ];
          };
        });
    };
}
