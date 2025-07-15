#!/bin/bash
# Usage: ./scripts/dev/create-profile.sh profile.json /path/in/ssm
set -euo pipefail
file="$1"
name="$2"
aws ssm put-parameter --name "$name" --type String --overwrite --value "$(cat "$file")"
