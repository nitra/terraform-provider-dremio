#!/bin/sh
# Canonical base64url without padding of compact JSON [role_id, member_type, member_id].
# A resource's computed id is this key; importing never resolves display names.
tofu import dremio_role_membership.reader '<canonical-membership-key>'
