# Security Policy

## Reporting A Vulnerability

Please do not report security vulnerabilities through public GitHub issues.

Email vulnerability reports to security@scenery.sh. Include:

- affected scenery version or commit SHA
- operating system and architecture
- reproduction steps or proof of concept
- impact and affected features
- any logs, traces, or command output that help reproduce the issue

We will acknowledge reports as soon as possible and coordinate disclosure with the reporter. Please give us a reasonable time to investigate and release a fix before publishing details.

## Scope

Security issues in the scenery CLI, application host (`scenery-kernel`), Worker ABI and language SDKs (Go, Rust, JS/TS, Python), local proxy, development runtime RPC, contract/client/OpenAPI generation, and repository release artifacts are in scope.

Issues in third-party services or applications built with scenery may be out of scope unless they demonstrate a vulnerability in scenery itself.

## Public Discussion

After a fix is available, we may publish a GitHub Security Advisory or release note with credit to the reporter unless they ask to remain anonymous.
