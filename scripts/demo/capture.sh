#!/bin/sh
# Record the demo tape and produce docs/assets/tui.png and docs/assets/demo.gif.
#
# Requires VHS and ffmpeg. Rebuilds the scratch world from scripts/demo/setup.sh;
# nothing outside /tmp/rexws-demo and docs/assets is written.
set -eu

ROOT=${REXWS_DEMO_DIR:-/tmp/rexws-demo}
REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)

"$REPO_ROOT/scripts/demo/setup.sh"

cd "$ROOT"
vhs "$REPO_ROOT/docs/assets/rexws-demo.tape"

# VHS can leave the scratch terminal's fake Rex streams and hold shell alive
# after the tape ends. Stop only processes rooted in the scratch world.
if command -v pkill >/dev/null 2>&1; then
  pkill -f "$ROOT/bin/rex" 2>/dev/null || true
  pkill -f "$ROOT/run.sh" 2>/dev/null || true
fi

ffmpeg -hide_banner -loglevel error -y \
  -i shots/02-expand.png \
  -vf 'crop=936:540:0:0' \
  -frames:v 1 shots/tui.png

ffmpeg -hide_banner -loglevel error -y \
  -i shots/raw.gif \
  -vf 'crop=936:540:0:0,fps=12,split[a][b];[a]palettegen=max_colors=128:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=3' \
  -loop 0 shots/demo.gif

mkdir -p "$REPO_ROOT/docs/assets"
cp shots/tui.png "$REPO_ROOT/docs/assets/tui.png"
cp shots/demo.gif "$REPO_ROOT/docs/assets/demo.gif"

# Dimension and size checks. Crop at 936 px, not 920: the rightmost `s` in
# `2 tabs` is clipped below this width.
png_dims=$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0 shots/tui.png)
gif_dims=$(ffprobe -v error -select_streams v:0 -show_entries stream=width,height -of csv=p=0 shots/demo.gif)
gif_fps=$(ffprobe -v error -select_streams v:0 -show_entries stream=r_frame_rate -of csv=p=0 shots/demo.gif)

test "$png_dims" = '936,540' || { echo "tui.png is $png_dims, want 936,540" >&2; exit 1; }
test "$gif_dims" = '936,540' || { echo "demo.gif is $gif_dims, want 936,540" >&2; exit 1; }
test "$gif_fps" = '12/1' || { echo "demo.gif is $gif_fps fps, want 12/1" >&2; exit 1; }
test "$(wc -c < shots/tui.png)" -le 204800 || { echo "tui.png exceeds 200 KB" >&2; exit 1; }
test "$(wc -c < shots/demo.gif)" -le 1048576 || { echo "demo.gif exceeds 1 MB" >&2; exit 1; }

printf 'tui.png %s bytes\n' "$(wc -c < shots/tui.png)"
printf 'demo.gif %s bytes\n' "$(wc -c < shots/demo.gif)"
