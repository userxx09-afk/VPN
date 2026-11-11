# Working on TapperVPN

## Nothing lives only on one PC

Everything needed to pick this project back up must be in the cloud:

- **Code, config, docs, scripts:** commit and push to this repo as each step
  is finished, not just at the end.
- **Secrets** (signing keys, server database and keys, tokens, passwords):
  never in this repo (it is public). Put them in the private store (see
  README, "What is not in the repo") as soon as they are made or changed,
  and say where.
- Before finishing, list anything that still exists only on one machine.

In November 2026 the original signing keys existed only on one PC and were
lost; every device had to be reinstalled by hand.

## Releases

Put the new version in `VERSION` and push to `main`:
`.github/workflows/release.yml` builds, signs and publishes every platform and
the Windows installer to the public releases-only repo `userxx09-afk/prod`
(`vpn-v<version>` plus the rolling `vpn-latest` that every device checks). A push that changes that workflow but not `VERSION` is a
dry run. The signing keys are the repository secrets `TAPPERVPN_RELEASE_KEY`
and `ANDROID_DEBUG_KEYSTORE`; publishing to prod uses `PROD_REPO_TOKEN`.
