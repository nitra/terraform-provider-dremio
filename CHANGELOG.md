# Changelog

All notable changes to this fork are documented here. Format loosely follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## Unreleased

### Added
- `dremio_source` now refuses to apply a metadata-impacting config change
  (anything Dremio's own `POST /apiv2/sources/isMetadataImpacting` flags -
  confirmed live to delete and rediscover every dataset under the source,
  silently dropping reflections/formats/permissions, with no warning at the
  API level) unless `DREMIO_ALLOW_METADATA_IMPACTING_CHANGE` is set in the
  environment running `tofu apply`.
- New `dremio_dataset` data source: resolves a dataset's current catalog id
  by path. Reference it from `dremio_raw_reflection`/`dremio_aggr_reflection`'s
  `dataset_id` instead of a hardcoded id so a reflection self-heals on the
  next `tofu apply` after an acknowledged metadata-impacting change, instead
  of being permanently orphaned against an id that no longer exists.
- `Importer` on `dremio_source`, so an existing source (e.g. created through
  the Dremio UI) can be brought under management with `tofu import` instead
  of being recreated.
- Unit tests for the GCS config mapping, the retry transport, and the
  not-found detection.
- CI workflow (`.github/workflows/ci.yml`): build, vet, test, and a docs
  drift check on every push/PR.
- Dependabot config for `gomod` and `github-actions`.
- Retrying HTTP transport (network errors, 429, 5xx) with capped exponential
  backoff, so a transient blip doesn't fail a whole `tofu apply`.

### Fixed
- `dremio_source` read no longer hard-fails when the source has been deleted
  out of band; it now clears the resource from state like other providers do,
  so the next plan proposes a recreate instead of erroring.
- `type` is validated against the set of source types this provider actually
  implements (`NAS`, `MSSQL`, `GCS`), instead of failing late and generically
  at apply time.
- `docs/` regenerated with `tfplugindocs`; it had never been updated since
  the initial scaffold and didn't reflect the real schema.
- `Makefile` fixed: `go test -i` (removed in Go 1.16), and `NAMESPACE`/
  `HOSTNAME`/`OS_ARCH` no longer hardcoded to the upstream repo and Windows.

## v0.5.0 - 2026-09-27
- Added `Importer` support for `dremio_source` (see Unreleased "Added" above;
  this was the first tagged release with it).

## v0.4.1 - 2026-09-27
- Release checksums are now GPG-signed with the `nitra` provider key
  registered at registry.opentofu.org.

## v0.4.0 - 2026-09-26
- Forked from [saltxwater/terraform-provider-dremio](https://github.com/saltxwater/terraform-provider-dremio).
- Added `GCS` support to `dremio_source` (`project_id`, `auth_mode`,
  `root_path`, `bucket_whitelist`, `async_enabled`, `caching_enable`,
  `cache_percent`, `client_email`, `client_id`, `private_key_id`,
  `secure_config.private_key`), matching `com.dremio.plugins.gcs.GCSConf` in
  dremio-oss.
- `go 1.16` -> `1.25.8`, `terraform-plugin-sdk/v2` `v2.4.4` -> `v2.40.1`, and
  the rest of the dependency graph bumped to clear known vulnerabilities.
