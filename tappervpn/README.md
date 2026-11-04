# TapperVPN

https://tailscale.com

Private WireGuard® networks made easy

## Overview

This repository contains the majority of TapperVPN's open source code.
Notably, it includes the `tappervpnd` daemon and
the `tappervpn` CLI tool. The `tappervpnd` daemon runs on Linux, Windows,
[macOS](https://tailscale.com/kb/1065/macos-variants/), and to varying degrees
on FreeBSD and OpenBSD. The TapperVPN iOS and Android apps use this repo's
code, but this repo doesn't contain the mobile GUI code.

Other [TapperVPN repos](https://github.com/orgs/tappervpn/repositories) of note:

* the Android app is at https://tappervpn.com/android
* the Synology package is at https://github.com/tailscale/tailscale-synology
* the QNAP package is at https://github.com/tailscale/tailscale-qpkg
* the Chocolatey packaging is at https://github.com/tailscale/tailscale-chocolatey

For background on which parts of TapperVPN are open source and why,
see [https://tailscale.com/opensource/](https://tailscale.com/opensource/).

## Using

We serve packages for a variety of distros and platforms at
[https://pkgs.tailscale.com](https://pkgs.tailscale.com/).

## Other clients

The [macOS, iOS, and Windows clients](https://tailscale.com/download)
use the code in this repository but additionally include small GUI
wrappers. The GUI wrappers on non-open source platforms are themselves
not open source.

## Building

We always require the latest Go release, currently Go 1.27. (While we build
releases with our [Go fork](https://github.com/tailscale/go/), its use is not
required.)

```
go install tappervpn.com/cmd/tappervpn{,d}
```

If you're packaging TapperVPN for distribution, use `build_dist.sh`
instead, to burn commit IDs and version info into the binaries:

```
./build_dist.sh tappervpn.com/cmd/tappervpn
./build_dist.sh tappervpn.com/cmd/tappervpnd
```

If your distro has conventions that preclude the use of
`build_dist.sh`, please do the equivalent of what it does in your
distro's way, so that bug reports contain useful version information.

## Bugs

Please file any issues about this code or the hosted service on
[the issue tracker](https://github.com/tailscale/tailscale/issues).

## Contributing

PRs welcome! But please file bugs. Commit messages should [reference
bugs](https://docs.github.com/en/github/writing-on-github/autolinked-references-and-urls).

We require [Developer Certificate of
Origin](https://en.wikipedia.org/wiki/Developer_Certificate_of_Origin)
`Signed-off-by` lines in commits.

See [commit-messages.md](docs/commit-messages.md) (or skim `git log`) for our commit message style.

## About Us

[TapperVPN](https://tailscale.com/) is primarily developed by the
people at https://github.com/orgs/tappervpn/people. For other contributors,
see:

* https://github.com/tailscale/tailscale/graphs/contributors
* https://tappervpn.com/android/graphs/contributors

## Legal

WireGuard is a registered trademark of Jason A. Donenfeld.
