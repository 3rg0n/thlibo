---
type: Module
title: internal/inferd
description: 9 go files; 29 exported symbols.
attributes:
  - { name: exported, value: "29" }
  - { name: files, value: "9" }
  - { name: package, value: inferd }
---
# internal/inferd

<!-- signpost:managed:summary -->
9 go files; 29 exported symbols.
<!-- /signpost:managed:summary -->

## Structure

<!-- signpost:managed:structure -->
9 files:
- `internal/inferd/addr.go`
- `internal/inferd/attachment_test.go`
- `internal/inferd/client.go`
- `internal/inferd/dial_unix.go`
- `internal/inferd/dial_windows.go`
- `internal/inferd/embed.go`
- `internal/inferd/inferd_test.go`
- `internal/inferd/protocol.go`
- `internal/inferd/timeout_test.go`

- **Exports** (29): `Attachment`, `Client`, `Client.Post`, `DefaultAdminAddress`, `DefaultEmbedAddress`, `DefaultGenerationAddress`, `DefaultTimeout`, `EmbedClient`, `EmbedClient.Embed`, `EmbedDimensions`, `EmbedTask`, `EmbedUnavailable`, `ErrBackendNotReady`, `JSONSchemaFormat`, `MaxFrameBytes`, `Message`, `Message.MarshalJSON`, `Request`, `Request.MarshalJSON`, `ResponseFormat`, `Result`, `Role`, `RoleAssistant`, `RoleSystem`, `RoleUser`, `TimeoutEnv`, `Tool`, `ToolCall`, `WireVersion`
<!-- /signpost:managed:structure -->

## Notes

_Anything written here is yours. signpost rewrites only the regions between its managed markers, and never this section._
