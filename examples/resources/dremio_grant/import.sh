#!/bin/sh
# Canonical base64url (no padding) of compact JSON:
# [object_type, object_id, grantee_type, grantee_id, privilege]
tofu import dremio_grant.analyst_select '<canonical-grant-key>'
