# Vendored fork of go-dremio-api-client

This is a copy of `github.com/saltxwater/go-dremio-api-client` v0.1.6, wired
in via a `replace` directive in the root `go.mod`, with one fix:

- `Reflection.Enabled` dropped its `omitempty` JSON tag. `omitempty` on a
  `bool` omits the field whenever it's `false` (Go's zero value), but
  Dremio's reflection API requires `enabled` to always be present in the
  request body and returns `400 "Reflections must have the enabled field
  set"` when it's missing. This broke `dremio_raw_reflection` and
  `dremio_aggr_reflection` any time a reflection was disabled
  (`enabled = false`), in both the original upstream code and every version
  of this fork before the fix.

The rest of the client is unmodified from v0.1.6. Everything not related to
reflections (sources, datasets, folders, spaces, catalog) still has similar
`bool ... json:"...,omitempty"` fields with the same latent bug
(`dataset.go`, `source.go`), but those aren't exercised by any resource
current tests cover - left alone here to keep this fix minimal and scoped to
the bug actually verified against live Dremio.
