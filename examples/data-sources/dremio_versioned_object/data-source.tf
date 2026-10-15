# Resolve the live Nessie content and source UID; never construct the RBAC ID by hand.
data "dremio_versioned_object" "sales" {
  object_type     = "PDS"
  path            = ["s", "a", "sales"]
  reference_type  = "BRANCH"
  reference_value = "main"
}

resource "dremio_grant" "sales_read" {
  object_type  = data.dremio_versioned_object.sales.grant_object_type
  object_id    = data.dremio_versioned_object.sales.id
  grantee_type = "ROLE"
  grantee_id   = dremio_role.analyst.id
  privilege   = "SELECT"
}
