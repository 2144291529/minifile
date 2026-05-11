# Changelog

## v0.1.0

Initial public release of the browser-based anonymous file transfer MVP.

### Added

- Anonymous room-based file transfer flow.
- Two-page browser UI:
  - `发送文件`
  - `下载文件`
- Custom room ID support, with automatic random room generation when omitted.
- Room password support, with automatic password generation when omitted.
- End-to-end encryption in the browser using password-derived secrets.
- Share link generation for room access.
- Room event timeline showing joins, leaves, uploads, downloads, and deletions.
- Relay-based resumable upload and download.
- Upload/download pause and resume controls.
- Transfer delete action.
- QUIC / HTTP3 relay transport with HTTPS fallback.
- WebSocket room snapshot updates.
- Local disk storage backend.
- Optional S3-compatible object storage backend wiring.
- Linux AMD64 binary build.
- Windows binary build.

### Changed

- Reworked the frontend from a single flow into separate send/download pages.
- Replaced modal-based room creation with direct inline room creation inputs.
- Switched upload/download progress display to percentage-based status.
- Improved room sharing flow so recipients can open a link or manually enter room ID and password.

### Notes

- Current release focuses on relay mode MVP.
- WebRTC P2P, STUN/TURN auto fallback, link quality selection, and performance tuning are planned for later versions.
