# Existing local-user profile is adopted by its stable UID separately.
# The role must be managed or imported; this does not create an account.
resource "dremio_role_membership" "reader" {
  role_id     = dremio_role.read_only.id
  member_type = "USER"
  member_id   = dremio_user.reader.id
}

# Nested assignment: analyst receives read_only privileges, not vice versa.
resource "dremio_role_membership" "analyst_reads" {
  role_id     = dremio_role.read_only.id
  member_type = "ROLE"
  member_id   = dremio_role.analyst.id
}
