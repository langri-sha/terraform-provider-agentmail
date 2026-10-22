#!/usr/bin/env bash
#
# Vendor AgentMail's OpenAPI document at a commit of agentmail-to/agentmail-docs,
# for the fake server's contract tests. Pass a commit SHA; without one, the
# latest commit that touched the document is used.
#
# Requires curl, and gh for the default.

set -euo pipefail

cd "$(dirname "$0")/.."

repo=agentmail-to/agentmail-docs
path=openapi/openapi.yml
sha="${1:-$(gh api "repos/${repo}/commits?path=${path}&per_page=1" --jq '.[0].sha')}"

curl -fsSL "https://raw.githubusercontent.com/${repo}/${sha}/${path}" \
  -o internal/fakeagentmail/testdata/openapi.yml
echo "https://github.com/${repo}/blob/${sha}/${path}" > internal/fakeagentmail/testdata/openapi.source
