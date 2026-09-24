# ForgeAI Release Packaging

W11 packages ForgeAI as a single static Go binary for four v0.1 targets:

- Linux amd64
- Linux arm64
- macOS arm64
- Windows amd64

The Docker image remains a separate supported deployment path.

## Build a local snapshot

Install a current GoReleaser v2 release, then run:

```bash
goreleaser check
goreleaser release --snapshot --clean
```

or:

```bash
make release-check
make release-snapshot
```

Expected artifacts are written to `dist/` with a `checksums.txt` file.

## Verify version metadata

Every release build injects the GoReleaser version into `internal/app.Version`.

After extracting the archive:

```bash
./forgeai version
```

A tagged v0.1.0 build must report `0.1.0` (GoReleaser strips the leading `v` in `.Version`).

## Clean-machine smoke path

After downloading and verifying the checksum:

```bash
./forgeai version
./forgeai init
export FORGEAI_MASTER_KEY='<value printed by init>'
./forgeai doctor
./forgeai serve
```

Then open the local server and follow `docs/DOGFOODING.md`.

A release artifact is not accepted merely because it builds. W11 requires the native archive to unpack, execute, initialize a clean data directory, pass `doctor`, and start the server using only documented steps.

## Automated local packaging smoke

```bash
./scripts/release-smoke.sh
```

The script validates the GoReleaser configuration, builds a snapshot, checks the exact four target archives and checksums, rejects unintended target archives, extracts the current host's supported archive when possible, and runs the packaged `forgeai version` command.

## Release discipline

- Do not publish or tag `v0.1.0` from W11.
- W12 owns the final tag.
- W12 also depends on #21 (Security & Privacy) and #26 (TechVit dogfooding).
- A GitHub Actions runner/account failure is not product acceptance evidence; these commands must remain runnable locally.
