# A tappervpnd peer for nix/testkit.nix: `hs-join KEY` joins the kit's control
# node, which is named headscale. Contract: nix/README.md.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  hs-join = pkgs.writeShellApplication {
    name = "hs-join";
    runtimeInputs = [ config.services.tappervpn.package ];
    text = ''
      key=''${1:?usage: hs-join KEY [tappervpn up flags]}
      shift
      # Returns once tappervpnd is up, so joining straight after boot can't race it.
      systemctl start tappervpnd.service
      # Broken control fails here instead of hanging the test to its global timeout.
      exec tappervpn up --timeout=60s --login-server http://headscale --auth-key "$key" "$@"
    '';
  };
in
{
  key = "headscale-testkit-peer";
  services.tappervpn.enable = true;
  # Interactive driver runs have a route out; keep their logs off TapperVPN's servers.
  services.tappervpn.disableUpstreamLogging = lib.mkDefault true;
  environment.systemPackages = [ hs-join ];
}
