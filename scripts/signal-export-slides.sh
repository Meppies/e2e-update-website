#!/bin/zsh
# Export the attachments of one Signal Desktop conversation (the group where the
# slides are shared) into a folder, for cmd/slides. Read-only: Signal itself is
# not changed. Needs Signal Desktop on this Mac with the group synced, and sigtop:
#   go install github.com/tbvdm/sigtop@latest
# Check `sigtop help export-attachments` first: options can change between versions.
#
# Usage: scripts/signal-export-slides.sh "Group name" ~/slides-in
set -e
GROUP="$1"; OUT="${2:-$HOME/slides-in}"
[ -n "$GROUP" ] || { echo "usage: $0 \"Group name\" [folder]"; exit 1; }
command -v sigtop >/dev/null || PATH="$PATH:$(go env GOPATH)/bin"
mkdir -p "$OUT"
sigtop export-attachments -c "$GROUP" "$OUT"
echo "Decks found:"
find "$OUT" -type f \( -iname '*.pdf' -o -iname '*.pptx' -o -iname '*.key' -o -iname '*.docx' \) | sed "s|$OUT/||"
