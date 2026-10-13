# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in this repository, please **do not** open a
public GitHub issue — that makes the vulnerability visible before a fix is available.

Instead, report it privately via
[GitHub's private vulnerability reporting](https://github.com/FelipeFuhr/ffreis-presentation-compiler/security/advisories/new).

## Response Timeline

| Severity | Acknowledgement | Fix Target |
|---|---|---|
| Critical / High | 48 hours | 14 days |
| Medium | 5 business days | 30 days |
| Low / Informational | 10 business days | Next minor release |

## Supported Versions

Only the latest release on `main` is actively maintained.
Security patches are not backported to older versions.

## Security Practices

- Secrets are scanned on every commit via `gitleaks` (run `make secrets-scan-staged` locally,
  or install lefthook hooks with `make setup`).
- Dependencies are kept up to date via Renovate (automated PRs on Monday mornings).
- CI workflows use minimally scoped permissions and OIDC for AWS access (no static keys).
