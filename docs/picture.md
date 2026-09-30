# Picture setup and on-screen display

## Picture size and position

Every consumer CRT crops the edges of the picture differently, and some sit off-centre. Settings → Video & Audio has four fields for this, stored under `[bridge.video]`:

- `picture_h_size` / `picture_v_size` (default `100`, range `80`–`100`) shrink the picture, in percent, until the edges your CRT was cutting off come into view. The freed border is black.
- `picture_h_offset` (pixels, `-72`–`72`) and `picture_v_offset` (field lines, `-28`–`28`) move the picture right/down (+) or left/up (−).

Changes apply on the next cast; saving restarts the current one.

To line the picture up by eye, press **Calibrate on CRT** while nothing is playing. The CRT shows a test pattern: a white border on the picture's edge, a crosshatch, a centre circle, and the 95% / 90% safe areas. Use the Position and Size keys (or the arrow keys; Shift for bigger steps) until the white border just shows on all four edges and the circle looks round; each change appears almost instantly. **Save** stores the values; **Cancel** discards them. A cast that starts mid-calibration takes over and keeps your unsaved values in the panel. An untouched calibration ends after 5 minutes.

## On-screen display

While a cast is playing, the bridge draws an old-TV-style OSD into the picture: a green volume bar (or red `MUTING`) when you turn the knob, a green channel banner when a cast starts (`CH 07` for a streams channel in the preset bank, otherwise the channel or source name, such as `PLEX`), and `PLAY ▶` / `FF ▶▶` / `REW ◀◀` on start, resume and seek. Each element fades after a few seconds and stays inside the title-safe area of a consumer CRT (inside the picture, if you have shrunk it).

Configure it under `[bridge.osd]`; every setting applies live, mid-cast:

- `enabled` (default `true`) turns the whole OSD on or off.
- `clock` (default `false`) shows the local time under the channel banner, with `clock_24h` for `21:41` instead of `9:41 PM`. In Docker, set the `TZ` environment variable (for example `TZ=America/New_York`) or the clock shows UTC.

The OSD appears only while something is playing; when paused or idle there is no picture to draw on.
