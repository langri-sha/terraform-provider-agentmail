# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Provider configuration: `api_key`, falling back to `AGENTMAIL_API_KEY`, and
  `base_url` for the EU API.
- `agentmail_inbox`, importable by address. Replacing or destroying an inbox
  warns that its mail goes with it, and `metadata` keeps its value types and
  updates by merge patch.
- `agentmail_list`, one authoritative allow or block list for the
  organization, a pod or an inbox, in the send, receive or reply direction.
  It adds entries before removing any and leaves read-only entries alone.
- The ephemeral `agentmail_api_key`, minting a scoped key with the
  `permissions` it must list and a `ttl` or `expires_at` without writing the
  secret to plan or state. It is revoked when the run ends unless
  `revoke_on_close = false`.
