#!/bin/sh
# Substitute the stable ID returned by the OSS role collection API.
tofu import dremio_role.read_only '<stable-role-id>'
