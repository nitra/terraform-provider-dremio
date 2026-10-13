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

Additional fork extensions include local-user profile support and
`rbac_role.go`: a client for the experimental OSS fork's `/api/v3/rbac/roles`
contract. It uses the existing authentication/request path, validates stable
role IDs and policy tags, and does not implement the Enterprise Roles API.
Roles require explicit ADMIN bootstrap. `rbac_membership.go` adds one exact
USER/ROLE assignment by stable IDs and a canonical import key. Bootstrap
remains outside this client extension. `rbac_grant.go` manages one
concrete explicit privilege by stable object/principal IDs, with canonical
import keys and exact revocation; it does not implement ALL or bulk scopes.
`rbac_ownership.go` reads durable ownership and transfers it by stable target and
owner IDs. Canonical import keys identify only the target. Delete supplies the
exact owner and policy tag, never deletes a live target or revokes grants, and
is only allowed by the server for proven-deleted namespace IDs. Existing ownership
requires explicit import; role/VDS creation already reserves durable owners.
One client's RBAC mutations are serialized before reading the shared policy tag.
The provider transport deliberately
does not retry OSS RBAC mutations after ambiguous failures.

Initial ADMIN is an operator-controlled, one-shot server operation, not provider
CRUD: the OSS fork exposes authenticated POST `/api/v3/rbac/bootstrap` only for
the active account whose UID is pinned in server startup configuration
`security.access-control.oss.bootstrap.admin-uid`. It uses KV CREATE and never
replaces an existing/corrupt policy. Remove that configuration after acceptance.
The provider does not create users/passwords/tokens or reset/bootstrap policy.
Import the already-existing `rbac:admin` USER membership before managing it with
`dremio_role_membership`. Normal RBAC resources require the initialized policy.
