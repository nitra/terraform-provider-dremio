# Vendored fork of go-dremio-api-client

This is a copy of `github.com/saltxwater/go-dremio-api-client` v0.1.6, wired
in via a `replace` directive in the root `go.mod`. The upstream client had
several `bool` fields tagged `json:"...,omitempty"`. Since a bool's zero
value is `false`, `omitempty` silently drops the field from the request body
whenever it's `false` - not just when it's genuinely absent/unset. All
occurrences of this bug found in the client are fixed here (fields listed
below now serialize `false` explicitly):

- `reflection.go`: `Reflection.Enabled`. Empirically verified against live
  Dremio: setting a reflection's `enabled` to `false` failed outright with
  `400 "Reflections must have the enabled field set"` - Dremio validates
  this field's presence explicitly. Affects `dremio_raw_reflection` and
  `dremio_aggr_reflection`.
- `source.go`: `Source.AccelerationNeverExpire`, `AccelerationNeverRefresh`.
  Empirically verified against live Dremio: unlike reflections, Dremio
  doesn't reject the missing field here, it silently resets it to the
  server's own default (`false`) on any `PUT`. This is worse than the
  reflection bug because it fails silently instead of erroring - a real
  source with either flag set to `true` (e.g. `accelerationNeverRefresh` was
  `true` on the real `bucket` dev source at the time this was found) would
  have had it silently flipped back to `false` by any `dremio_source` Update
  whose Go-side value for that field happened to be `false` at the time.
  Affects `dremio_source`, which is migrated to terraform-plugin-framework
  and manages real resources in `tofu/dremio-dev`.
- `dataset.go`: `PhysicalDatasetFormat.{SkipFirstLine, ExtractHeader,
  TrimHeader, AutoGenerateColumnNames, HasMergedCells}` and
  `DatasetAccelerationRefreshPolicy.{NeverExpire, NeverRefresh}`. Same bug
  pattern, fixed by inspection rather than live verification -
  `dremio_physical_dataset` isn't migrated off SDKv2 and isn't exercised by
  any real state, so there was nothing safe to verify this against without
  standing up a dedicated promoted-dataset test scenario disproportionate to
  how little this resource is used.

The rest of the client is unmodified from v0.1.6.
