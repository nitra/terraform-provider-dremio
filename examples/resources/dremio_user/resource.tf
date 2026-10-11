# Existing local users must be imported by UUID. Passwords and permissions
# remain under their existing identity-management workflow.
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
