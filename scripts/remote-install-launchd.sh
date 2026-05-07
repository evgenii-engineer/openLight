#!/usr/bin/env bash
# Generate a per-user LaunchAgent plist for the agent and install it at
# LAUNCH_AGENT on the remote Mac mini. Validates the result with plutil.
set -euo pipefail

: "${SSH_TARGET:?SSH_TARGET must be set}"
: "${RUNTIME_DIR:?RUNTIME_DIR must be set}"
: "${PROJECT_DIR:?PROJECT_DIR must be set}"
: "${CONFIG_REMOTE:?CONFIG_REMOTE must be set}"
: "${LAUNCH_AGENT:?LAUNCH_AGENT must be set}"

cat <<PLIST | ssh "${SSH_TARGET}" "cat > '${LAUNCH_AGENT}' && plutil -lint '${LAUNCH_AGENT}'"
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>dev.openlight.agent</string>
  <key>ProgramArguments</key>
  <array>
    <string>${RUNTIME_DIR}/bin/openlight</string>
    <string>agent</string>
    <string>-config</string>
    <string>${CONFIG_REMOTE}</string>
  </array>
  <key>WorkingDirectory</key>
  <string>${PROJECT_DIR}</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>${RUNTIME_DIR}/logs/agent.out.log</string>
  <key>StandardErrorPath</key>
  <string>${RUNTIME_DIR}/logs/agent.err.log</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
  </dict>
</dict>
</plist>
PLIST
