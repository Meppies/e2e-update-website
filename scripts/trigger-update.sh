#!/bin/zsh
# Starts the "Update agenda" workflow on GitHub every time launchd calls it (every 5 minutes).
# GitHub's own schedule is best effort and often hours late, so this Mac presses the button instead.
# Stops itself after 19 October 2026 (Amsterdam time).
LOG=~/Library/Logs/e2e-agenda-trigger.log
LABEL=eu.meppie.e2e-agenda-trigger
if [ "$(TZ=Europe/Amsterdam date +%Y%m%d)" -gt 20261019 ]; then
  echo "$(date -u +%FT%TZ) event over, unloading $LABEL" >> $LOG
  launchctl bootout gui/$(id -u)/$LABEL 2>/dev/null
  exit 0
fi
if /opt/homebrew/bin/gh workflow run update.yml -R Meppies/e2e-update-website >> $LOG 2>&1; then
  echo "$(date -u +%FT%TZ) triggered" >> $LOG
else
  echo "$(date -u +%FT%TZ) FAILED (exit $?)" >> $LOG
fi
