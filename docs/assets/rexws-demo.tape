# rexws demo tape.
#
# Scratch only: run this from /tmp/rexws-demo after scripts/demo/setup.sh.
# It launches scripts/demo/run.sh, which sets REXWS_CONFIG_DIR and
# REXWS_REX_BIN to the scratch world and forces an 80x24 PTY. The fake Rex
# never touches a real session or worktree. Do not run against a real config.
Output shots/raw.gif
Set Shell bash
Set Width 1200
Set Height 700
Set FontSize 18
Set Padding 0
Set Margin 0
Set Theme { "background": "#1E1E1E", "foreground": "#E5E7EB", "black": "#1E1E1E", "red": "#F87171", "green": "#4ADE80", "yellow": "#FDBA74", "blue": "#7AA2F7", "purple": "#C084FC", "cyan": "#22D3EE", "white": "#E5E7EB", "brightBlack": "#94A3B8", "brightRed": "#F87171", "brightGreen": "#4ADE80", "brightYellow": "#FDBA74", "brightBlue": "#7AA2F7", "brightPurple": "#C084FC", "brightCyan": "#22D3EE", "brightWhite": "#F8FAFC" }
Hide
Type "REXWS_DEMO_HOLD=1 /tmp/rexws-demo/run.sh"
Enter
Sleep 2s
Show
Sleep 2.5s
Screenshot shots/01-list.png
Type "l"
Sleep 500ms
Screenshot shots/02-expand.png
Sleep 200ms
Type "l"
Sleep 700ms
Screenshot shots/03-child.png
Type "h"
Sleep 250ms
Type "h"
Sleep 450ms
Type "jjjj"
Sleep 700ms
Screenshot shots/04-main.png
Enter
Sleep 400ms
Screenshot shots/05-switching.png
Sleep 1300ms
Screenshot shots/06-live.png
Sleep 1200ms
Type "n"
Sleep 700ms
Type "feat/search"
Sleep 500ms
Screenshot shots/07-new.png
Ctrl+[
Sleep 400ms
Type "c"
Sleep 3s
Screenshot shots/08-cleanup.png
Space
Sleep 500ms
Screenshot shots/09-selected.png
Enter
Sleep 900ms
Screenshot shots/10-review.png
Ctrl+[
Sleep 400ms
Ctrl+[
Sleep 400ms
Type "q"
Sleep 400ms
