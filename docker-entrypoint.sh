#!/bin/sh
set -e

CONFIG_FILE="/app/config/config.yaml"
CONFIG_EXAMPLE="/app/config.yaml.example"

if [ ! -f "$CONFIG_FILE" ]; then
  echo "[all2api] config.yaml not found, copying from config.yaml.example..."
  mkdir -p /app/config
  cp "$CONFIG_EXAMPLE" "$CONFIG_FILE"
  echo "[all2api] config.yaml created at $CONFIG_FILE"
fi

exec /app/all2api -config "$CONFIG_FILE"
