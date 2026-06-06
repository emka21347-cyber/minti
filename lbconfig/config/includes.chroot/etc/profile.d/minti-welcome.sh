#!/bin/sh
# Run minti-fetch on interactive login shells
case "$-" in
    *i*) command -v minti-fetch >/dev/null 2>&1 && minti-fetch ;;
esac
