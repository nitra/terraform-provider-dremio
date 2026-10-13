# Experimental OSS RBAC fork only. Bootstrap an active ADMIN explicitly first.
# Existing roles must be imported by stable ID, never adopted by name.
resource "dremio_role" "read_only" {
  name = "ReadOnly"

  lifecycle {
    prevent_destroy = true
  }
}

resource "dremio_role" "analyst" {
  name = "Analyst"

  lifecycle {
    prevent_destroy = true
  }
}

# Memberships and privileges are not part of these resources.
# Do not add this example to production before the OSS API is deployed/accepted.
