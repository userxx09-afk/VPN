# sshtest

This contains packages that are forked & locally hacked up for use
in tests.

Notably, `golang.org/x/crypto/ssh` was copied to
`tappervpn.com/tempfork/sshtest/ssh` to permit adding behaviors specific
to testing (for testing TapperVPN SSH) that aren't necessarily desirable
to have upstream.
