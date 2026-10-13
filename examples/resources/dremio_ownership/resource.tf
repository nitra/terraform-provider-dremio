# Import durable ownership first if it already exists (including roles and VDS).
resource "dremio_ownership" "analytics" {
  object_type = "SPACE"
  object_id   = "stable-space-id"
  owner_type  = "ROLE"
  owner_id    = "stable-analyst-role-id"

  lifecycle {
    prevent_destroy = true
  }
}

# Changing owner_type/owner_id transfers ownership in place.
# To stop managing a live target, explicitly forget state without destroying it.
# Cleanup destroy is only permitted after the namespace target ID is deleted.
# This does not delete the object or revoke any grants.
