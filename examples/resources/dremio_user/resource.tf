# Requires OpenTofu or Terraform 1.11+ for the write-only password argument.
# Pass the password through an ephemeral value; it never enters plan or state,
# so hand it to the user out of band (for example via your secret manager).
variable "new_user_password" {
  type      = string
  ephemeral = true
  sensitive = true
}

# Create a new local user.
resource "dremio_user" "new" {
  name                = "new-user"
  first_name          = "New"
  last_name           = "User"
  email               = "new-user@example.com"
  initial_password_wo = var.new_user_password
}

# Or manage an existing local user: import it by UUID. No password is needed,
# and permissions stay under their existing identity-management workflow.
resource "dremio_user" "existing" {
  name = "existing-user"

  lifecycle {
    prevent_destroy = true
  }
}

import {
  to = dremio_user.existing
  id = "b9dbebc7-bc3b-4d56-9154-31762ab65a43"
}
