# Manage only this SELECT grant; other/inherited privileges are untouched.
resource "dremio_grant" "analyst_select" {
  object_type  = "VDS"
  object_id    = dremio_virtual_dataset.report.id
  grantee_type = "ROLE"
  grantee_id   = dremio_role.analyst.id
  privilege   = "SELECT"
}

# SYSTEM is represented by the existing PROJECT enum, not a new object type.
resource "dremio_grant" "create_roles" {
  object_type  = "PROJECT"
  object_id    = "SYSTEM"
  grantee_type = "ROLE"
  grantee_id   = dremio_role.analyst.id
  privilege   = "CREATE_ROLE"
}
