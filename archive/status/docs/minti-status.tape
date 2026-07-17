# vhs source for the README .gif. Render with:
#
#   make status-gif                 # uses this file → status/docs/minti-status.gif
#   vhs status/docs/minti-status.tape
#
# Install vhs from https://github.com/charmbracelet/vhs (or `brew install
# vhs` on macOS). vhs needs ttyd + ffmpeg available on PATH at render
# time. The .gif itself is a build artefact, never committed.
#
# The recording assumes:
#   - minti-status is installed at /usr/bin/minti-status (or on PATH)
#   - minti-cland is running locally with a Clan formed, OR not (the
#     panel degrades gracefully to (unaffiliated))
#
# Goal: ~8 seconds of stable rendering — long enough that the fast
# (2s) ticker fires three times so a reviewer of the .gif can see the
# refresh badge tick.

Output status/docs/minti-status.gif

Set FontFamily "JetBrains Mono"
Set FontSize 13
Set Width 1400
Set Height 760
Set Padding 16
Set Theme "Catppuccin Mocha"
Set TypingSpeed 25ms
Set Margin 0
Set MarginFill "#1e1e2e"

# Banner: short title before the recording starts. Helps when the .gif
# is embedded as a doc asset without surrounding caption.
Hide
Type@0 "clear"
Enter
Show

Type "minti-status"
Enter

# Let the first frame paint + give the fast ticker time to fire twice.
Sleep 8s

# Quit cleanly so the last frame shows the terminal prompt returned,
# proving it didn't crash.
Type "q"
Sleep 800ms
