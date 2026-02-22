#!/bin/bash
# generate-docs.sh — Auto-generate documentation from the InteractKit SDK source.
# Outputs markdown files into the docs/ submodule.
#
# Usage:
#   ./scripts/generate-docs.sh
#
# What it generates:
#   - docs/docs/reference/events.md       — All event types from events/
#   - docs/docs/reference/interfaces.md   — Core interfaces from core/
#   - docs/docs/reference/config.md       — Config structs from factories/ and handlers/
#   - docs/docs/services/providers.md     — Service implementations and their configs

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DOCS_DIR="$ROOT/docs/docs/reference"

mkdir -p "$DOCS_DIR"
mkdir -p "$ROOT/docs/docs/services"

###############################################################################
# 1. Events Reference
###############################################################################
generate_events() {
  local out="$DOCS_DIR/events.md"
  cat > "$out" <<'HEADER'
# Event Reference

All event types that flow through the InteractKit pipeline.

> Auto-generated from source. Do not edit manually.

HEADER

  # Process each event directory
  for dir in "$ROOT"/events/*/; do
    [ -d "$dir" ] || continue
    local category
    category=$(basename "$dir")
    echo "## ${category^} Events" >> "$out"
    echo "" >> "$out"

    for file in "$dir"*.go; do
      [ -f "$file" ] || continue

      # Extract struct definitions with their comments
      awk '
        /^\/\// { comment = comment $0 "\n"; next }
        /^type [A-Z][A-Za-z]*Event struct/ {
          name = $2
          if (comment != "") {
            gsub(/^\/\/ ?/, "", comment)
            printf "### `%s`\n\n%s\n", name, comment
          } else {
            printf "### `%s`\n\n", name
          }

          # Read struct fields
          printf "```go\n"
          printf "type %s struct {\n", name
          getline  # skip opening brace
          while (getline > 0) {
            if (/^\}/) { break }
            printf "%s\n", $0
          }
          printf "}\n```\n\n"
          comment = ""
          next
        }
        { comment = "" }
      ' "$file" >> "$out"
    done
  done

  # Also extract base events from core/
  echo "## Core Events" >> "$out"
  echo "" >> "$out"
  awk '
    /^\/\// { comment = comment $0 "\n"; next }
    /^type [A-Z][A-Za-z]*Event struct/ {
      name = $2
      if (comment != "") {
        gsub(/^\/\/ ?/, "", comment)
        printf "### `%s`\n\n%s\n", name, comment
      } else {
        printf "### `%s`\n\n", name
      }
      printf "```go\n"
      printf "type %s struct {\n", name
      getline
      while (getline > 0) {
        if (/^\}/) { break }
        printf "%s\n", $0
      }
      printf "}\n```\n\n"
      comment = ""
      next
    }
    { comment = "" }
  ' "$ROOT"/core/base_events.go >> "$out"

  echo "  -> Generated $out"
}

###############################################################################
# 2. Core Interfaces Reference
###############################################################################
generate_interfaces() {
  local out="$DOCS_DIR/interfaces.md"
  cat > "$out" <<'HEADER'
# Interface Reference

Core interfaces that define the InteractKit handler and service contracts.

> Auto-generated from source. Do not edit manually.

HEADER

  for file in "$ROOT"/core/*.go; do
    [ -f "$file" ] || continue

    awk '
      /^\/\// { comment = comment $0 "\n"; next }
      /^type [A-Z][A-Za-z]* interface/ {
        name = $2
        if (comment != "") {
          gsub(/^\/\/ ?/, "", comment)
          printf "## `%s`\n\n%s\n", name, comment
        } else {
          printf "## `%s`\n\n", name
        }
        printf "```go\n"
        printf "type %s interface {\n", name
        getline
        while (getline > 0) {
          if (/^\}/) { break }
          printf "%s\n", $0
        }
        printf "}\n```\n\n"
        comment = ""
        next
      }
      { comment = "" }
    ' "$file" >> "$out"
  done

  echo "  -> Generated $out"
}

###############################################################################
# 3. Config Structs Reference
###############################################################################
generate_configs() {
  local out="$DOCS_DIR/config.md"
  cat > "$out" <<'HEADER'
# Configuration Reference

All configuration structs used to set up handlers, services, and the pipeline.

> Auto-generated from source. Do not edit manually.

HEADER

  # Factory configs
  echo "## Factory Configuration" >> "$out"
  echo "" >> "$out"
  for file in "$ROOT"/factories/*.go; do
    [ -f "$file" ] || continue
    awk '
      /^\/\// { comment = comment $0 "\n"; next }
      /^type [A-Z][A-Za-z]*(Config|Keys) struct/ {
        name = $2
        if (comment != "") {
          gsub(/^\/\/ ?/, "", comment)
          printf "### `%s`\n\n%s\n", name, comment
        } else {
          printf "### `%s`\n\n", name
        }
        printf "```go\n"
        printf "type %s struct {\n", name
        getline
        while (getline > 0) {
          if (/^\}/) { break }
          printf "%s\n", $0
        }
        printf "}\n```\n\n"
        comment = ""
        next
      }
      { comment = "" }
    ' "$file" >> "$out"
  done

  # Handler configs
  echo "## Handler Configuration" >> "$out"
  echo "" >> "$out"
  for file in "$ROOT"/handlers/*/*config*.go "$ROOT"/handlers/context/task_group.go; do
    [ -f "$file" ] || continue
    local rel
    rel=$(echo "$file" | sed "s|$ROOT/||")
    echo "<!-- source: $rel -->" >> "$out"
    awk '
      /^\/\// { comment = comment $0 "\n"; next }
      /^type [A-Z][A-Za-z]*(Config|Variable|Def|GroupConfig) struct/ {
        name = $2
        if (comment != "") {
          gsub(/^\/\/ ?/, "", comment)
          printf "### `%s`\n\n%s\n", name, comment
        } else {
          printf "### `%s`\n\n", name
        }
        printf "```go\n"
        printf "type %s struct {\n", name
        getline
        while (getline > 0) {
          if (/^\}/) { break }
          printf "%s\n", $0
        }
        printf "}\n```\n\n"
        comment = ""
        next
      }
      { comment = "" }
    ' "$file" >> "$out"
  done

  echo "  -> Generated $out"
}

###############################################################################
# 4. Service Providers Reference
###############################################################################
generate_services() {
  local out="$ROOT/docs/docs/services/providers.md"
  cat > "$out" <<'HEADER'
# Service Provider Reference

Configuration options for each supported service provider.

> Auto-generated from source. Do not edit manually.

HEADER

  for provider_dir in "$ROOT"/services/*/; do
    [ -d "$provider_dir" ] || continue
    local provider
    provider=$(basename "$provider_dir")
    echo "## ${provider^}" >> "$out"
    echo "" >> "$out"

    for svc_dir in "$provider_dir"*/; do
      [ -d "$svc_dir" ] || continue
      local svc_type
      svc_type=$(basename "$svc_dir")
      echo "### ${svc_type^^}" >> "$out"
      echo "" >> "$out"

      for file in "$svc_dir"*.go; do
        [ -f "$file" ] || continue
        # Extract Config structs
        awk '
          /^\/\// { comment = comment $0 "\n"; next }
          /^type [A-Z][A-Za-z]*Config struct/ {
            name = $2
            if (comment != "") {
              gsub(/^\/\/ ?/, "", comment)
              printf "%s\n", comment
            }
            printf "```go\n"
            printf "type %s struct {\n", name
            getline
            while (getline > 0) {
              if (/^\}/) { break }
              printf "%s\n", $0
            }
            printf "}\n```\n\n"
            comment = ""
            next
          }
          { comment = "" }
        ' "$file" >> "$out"
      done
    done
  done

  # Transport providers
  echo "## Transports" >> "$out"
  echo "" >> "$out"
  for transport_dir in "$ROOT"/transports/*/; do
    [ -d "$transport_dir" ] || continue
    local transport
    transport=$(basename "$transport_dir")
    echo "### ${transport^}" >> "$out"
    echo "" >> "$out"

    for file in "$transport_dir"*.go; do
      [ -f "$file" ] || continue
      awk '
        /^\/\// { comment = comment $0 "\n"; next }
        /^type [A-Z][A-Za-z]*Config struct/ || /^type Config struct/ {
          name = $2
          if (comment != "") {
            gsub(/^\/\/ ?/, "", comment)
            printf "%s\n", comment
          }
          printf "```go\n"
          printf "type %s struct {\n", name
          getline
          while (getline > 0) {
            if (/^\}/) { break }
            printf "%s\n", $0
          }
          printf "}\n```\n\n"
          comment = ""
          next
        }
        { comment = "" }
      ' "$file" >> "$out"
    done
  done

  echo "  -> Generated $out"
}

###############################################################################
# Run all generators
###############################################################################
echo "Generating documentation from source..."
generate_events
generate_interfaces
generate_configs
generate_services
echo ""
echo "Done! Generated docs are in docs/docs/"
