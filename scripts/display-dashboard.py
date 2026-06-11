#!/usr/bin/env python3
"""
openLight MHS35 status dashboard — 480×320 landscape.

Layout:
  Header  — title + clock
  Top row — Pi metrics (left) | Mac mini metrics (right)
  Mid row — Brain status (left) | Edge services (right)
  Footer  — hostname + last-updated

Requires: python3-pil  (sudo apt install python3-pil)
"""

import json
import signal
import subprocess
import textwrap
import time
import os
import sys
import urllib.request
import urllib.error
from datetime import datetime
from pathlib import Path

try:
    from PIL import Image, ImageDraw, ImageFont
except ImportError:
    print("python3-pil required: sudo apt install python3-pil", file=sys.stderr)
    sys.exit(1)

# ── config ────────────────────────────────────────────────────────────────────

OPENLIGHT_BIN       = os.environ.get("OPENLIGHT_BIN",       "/home/damk/bin/openlight")
OPENLIGHT_CFG       = os.environ.get("OPENLIGHT_CFG",       "/etc/openlight/agent.yaml")
OPENLIGHT_FB        = os.environ.get("OPENLIGHT_FB",        "/dev/fb0")
OPENLIGHT_BRAIN_URL = os.environ.get("OPENLIGHT_BRAIN_URL", "http://openclaw-m1:8787")
OPENLIGHT_NODE_ID   = os.environ.get("OPENLIGHT_NODE_ID",   "raspberry-pi-5")
REFRESH_SEC         = 600
MSG_FILE            = "/tmp/openlight-display-msg.json"

W, H = 480, 320

# ── palette ───────────────────────────────────────────────────────────────────

BG     = (10,  12,  28)
PANEL  = (18,  22,  45)
BORDER = (40,  60, 120)
TITLE  = (80, 180, 255)
FG     = (210, 215, 230)
DIM    = ( 90,  95, 120)
GREEN  = ( 60, 210, 100)
YELLOW = (255, 200,  50)
RED    = (255,  70,  70)
CYAN   = ( 60, 210, 200)
ORANGE = (255, 150,  50)
PURPLE = (180, 100, 255)

# ── fonts ─────────────────────────────────────────────────────────────────────

def _ttf(size):
    for p in [
        "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
        "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
        "/usr/share/fonts/truetype/liberation/LiberationMono-Regular.ttf",
        "/usr/share/fonts/truetype/freefont/FreeMono.ttf",
    ]:
        if Path(p).exists():
            return ImageFont.truetype(p, size)
    return ImageFont.load_default()

F10 = _ttf(10)
F11 = _ttf(11)
F12 = _ttf(12)
F13 = _ttf(13)
F14 = _ttf(14)
F18 = _ttf(18)
F20 = _ttf(20)
F24 = _ttf(24)
F32 = _ttf(32)

# ── framebuffer ───────────────────────────────────────────────────────────────

def push(img):
    rgb = img.convert("RGB")
    px  = rgb.load()
    buf = bytearray(W * H * 2)
    i   = 0
    for y in range(H):
        for x in range(W):
            r, g, b = px[x, y]
            w = ((r & 0xF8) << 8) | ((g & 0xFC) << 3) | (b >> 3)
            buf[i]   = w & 0xFF
            buf[i+1] = (w >> 8) & 0xFF
            i += 2
    try:
        with open(OPENLIGHT_FB, "wb") as fb:
            fb.write(bytes(buf))
    except OSError as e:
        print(f"[fb] {e}", file=sys.stderr)

# ── data fetchers ─────────────────────────────────────────────────────────────

def fetch_local_status():
    """Return dict with Pi stats from openlight cli /status output."""
    out = {}
    try:
        r = subprocess.run(
            [OPENLIGHT_BIN, "cli", "--config", OPENLIGHT_CFG, "--exec", "/status"],
            capture_output=True, text=True, timeout=15,
        )
        for line in r.stdout.splitlines():
            if line.startswith("time="):
                continue
            k, _, v = line.strip().partition(":")
            k, v = k.strip().lower(), v.strip()
            out[k] = v
    except Exception:
        pass
    return out


def fetch_agent_active():
    """Check if openlight-agent systemd service is active."""
    try:
        r = subprocess.run(
            ["systemctl", "is-active", "openlight-agent"],
            capture_output=True, text=True, timeout=3,
        )
        return r.stdout.strip() == "active"
    except Exception:
        return False


def fetch_pi_services():
    """Return list of (name, status_str, ok_bool) for local Pi services."""
    services = []

    # tailscale — systemd unit
    try:
        r = subprocess.run(["systemctl", "is-active", "tailscaled"],
                           capture_output=True, text=True, timeout=3)
        active = r.stdout.strip() == "active"
        services.append(("tailscale", "up" if active else "down", active))
    except Exception:
        services.append(("tailscale", "?", False))

    # synapse — docker compose
    try:
        r = subprocess.run(
            ["docker", "compose", "-f", "/home/damk/matrix/docker-compose.yml",
             "ps", "--format", "{{.Service}}:{{.State}}"],
            capture_output=True, text=True, timeout=8,
        )
        if r.returncode == 0 and r.stdout.strip():
            running = all(
                "running" in line.lower()
                for line in r.stdout.strip().splitlines()
                if line.strip()
            )
            services.append(("synapse", "up" if running else "partial", running))
        else:
            services.append(("synapse", "down", False))
    except Exception:
        services.append(("synapse", "?", False))

    return services


def _http_get(url, timeout=5):
    req = urllib.request.Request(url, method="GET")
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read().decode())


class BrainInfo:
    online          = False
    node_id         = ""
    model           = ""
    fast_model      = ""
    latency_ms      = 0.0
    smart_latency_ms = 0.0
    fast_latency_ms  = 0.0
    cpu_pct         = 0.0
    mem_used        = 0.0
    mem_total       = 0.0
    uptime_s        = 0
    error           = ""

    def __init__(self, base_url):
        t0 = time.monotonic()
        try:
            h = _http_get(base_url + "/health", timeout=4)
            self.latency_ms       = (time.monotonic() - t0) * 1000
            self.online           = h.get("status") == "ok"
            self.node_id          = h.get("node_id", "")
            self.model            = h.get("model", "")
            self.fast_model       = h.get("fast_model", "")
            self.smart_latency_ms = float(h.get("smart_latency_ms", 0))
            self.fast_latency_ms  = float(h.get("fast_latency_ms", 0))
            self.uptime_s         = int(h.get("uptime_s", 0))
        except Exception as e:
            self.latency_ms = (time.monotonic() - t0) * 1000
            self.error = str(e)
            return

        if self.online:
            try:
                s = _http_get(base_url + "/system", timeout=6)
                self.cpu_pct   = s.get("cpu_pct", 0.0)
                self.mem_used  = s.get("mem_used_gb", 0.0)
                self.mem_total = s.get("mem_total_gb", 0.0)
            except Exception:
                pass

# ── helpers ───────────────────────────────────────────────────────────────────

def uptime_str(seconds):
    if seconds <= 0:
        return "—"
    h, rem = divmod(int(seconds), 3600)
    m = rem // 60
    if h >= 24:
        return f"{h//24}d {h%24}h"
    if h:
        return f"{h}h {m}m"
    return f"{m}m"


def _state_color(s):
    s = s.lower()
    if s in ("running", "ok", "connected", "online", "active"):
        return GREEN
    if s in ("stopped", "offline", "unknown", "inactive"):
        return YELLOW
    return RED


def _trim(d, text, max_px, font):
    while text and d.textlength(text, font=font) > max_px:
        text = text[:-1]
    return text


def _cpu_color(pct):
    if pct < 50:  return GREEN
    if pct < 80:  return YELLOW
    return RED


def _mem_color(used, total):
    if total <= 0: return FG
    pct = used / total
    if pct < 0.7:  return CYAN
    if pct < 0.9:  return YELLOW
    return RED

# ── drawing primitives ────────────────────────────────────────────────────────

def panel(d, x, y, w, h, fill=PANEL, outline=BORDER):
    d.rounded_rectangle([x, y, x+w, y+h], radius=5, fill=fill, outline=outline)

def lbl(d, x, y, text, font=F11, color=DIM):
    d.text((x, y), text, font=font, fill=color)

def val(d, x, y, text, font=F18, color=FG):
    d.text((x, y), text, font=font, fill=color)

def dot(d, x, y, color=GREEN, r=4):
    d.ellipse([x-r, y-r, x+r, y+r], fill=color)

# ── main screen ───────────────────────────────────────────────────────────────

HEADER_H = 28
FOOTER_H = 18
PAD      = 4
COL_W    = (W - PAD * 3) // 2   # ~235
NODE_H   = 130                   # height of each node panel
SVCBAR_H = H - HEADER_H - NODE_H - FOOTER_H - PAD * 4

# node panel y positions
NODE_Y = HEADER_H + PAD


def draw_node_panel(d, x, y, w, h, title, title_color,
                    cpu, mem_used, mem_total, uptime_s, extra_rows=None):
    """Draw a compact stats panel for one node."""
    panel(d, x, y, w, h)

    # title
    lbl(d, x+8, y+4, title, font=F12, color=title_color)

    row_y = y + 20
    row_h = (h - 24) // 4

    def row(i, label_text, value_text, vcolor=FG, sub=""):
        ry = row_y + i * row_h
        lbl(d, x+8, ry, label_text)
        d.text((x+8, ry+12), value_text, font=F14, fill=vcolor)
        if sub:
            lbl(d, x+8, ry+26, sub, font=F10, color=DIM)

    cpu_str = f"{cpu:.1f}%" if cpu > 0 else "—"
    mem_str = f"{mem_used:.1f} GiB" if mem_used > 0 else "—"
    mem_sub = f"{mem_used:.1f} / {mem_total:.1f} GiB" if mem_total > 0 else ""

    row(0, "CPU",    cpu_str,  _cpu_color(cpu))
    row(1, "Memory", mem_str,  _mem_color(mem_used, mem_total), sub=mem_sub)
    row(2, "Uptime", uptime_str(uptime_s), vcolor=FG)

    if extra_rows:
        for i, (lbl_t, val_t, col) in enumerate(extra_rows, start=3):
            row(i, lbl_t, val_t, col)


def build_status(local, brain, agent_active, pi_services=None):
    img = Image.new("RGB", (W, H), BG)
    d   = ImageDraw.Draw(img)

    # ── header ───────────────────────────────────────────────────────────────
    d.rectangle([0, 0, W, HEADER_H], fill=(14, 17, 38))
    d.text((8, 5), "openLight", font=F18, fill=TITLE)
    ts = datetime.now().strftime("%H:%M:%S")
    tw = d.textlength(ts, font=F13)
    d.text((W - tw - 6, 7), ts, font=F13, fill=DIM)
    d.line([(0, HEADER_H), (W, HEADER_H)], fill=BORDER, width=1)

    # ── Pi panel (left) ───────────────────────────────────────────────────────
    pi_x = PAD
    pi_y = NODE_Y

    # Parse Pi stats
    pi_cpu = 0.0
    try:
        pi_cpu = float(local.get("cpu", "0").replace("%", "").strip())
    except Exception:
        pass
    pi_mem_str = local.get("memory", "")
    pi_mem_used, pi_mem_total = 0.0, 0.0
    if "/" in pi_mem_str:
        parts = pi_mem_str.split("/")
        try:
            pi_mem_used  = float(parts[0].strip().split()[0])
            pi_mem_total = float(parts[1].strip().split()[0])
        except Exception:
            pass
    pi_uptime = local.get("uptime", "—")
    pi_temp   = local.get("cpu temp", "—")

    draw_node_panel(d, pi_x, pi_y, COL_W, NODE_H,
                    f"🍓 {OPENLIGHT_NODE_ID}",
                    ORANGE,
                    pi_cpu, pi_mem_used, pi_mem_total, 0,
                    extra_rows=[("Temp", pi_temp, ORANGE),
                                ("Uptime", pi_uptime, FG)])

    # override row 2 (uptime) and row 3 (temp) manually since draw_node_panel
    # uses uptime_s but Pi gives string — just re-draw rows 2+3 over the panel
    row_y = pi_y + 20
    row_h = (NODE_H - 24) // 4
    lbl(d, pi_x+8, row_y + 2*row_h,       "Uptime")
    d.text((pi_x+8, row_y + 2*row_h + 12), pi_uptime, font=F14, fill=FG)
    lbl(d, pi_x+8, row_y + 3*row_h,       "Temp")
    d.text((pi_x+8, row_y + 3*row_h + 12), pi_temp,   font=F14, fill=ORANGE)

    # ── Mac mini panel (right) ────────────────────────────────────────────────
    mac_x = PAD*2 + COL_W
    mac_y = NODE_Y

    if brain.online:
        mac_uptime = brain.uptime_s
        draw_node_panel(d, mac_x, mac_y, COL_W, NODE_H,
                        f"🧠 {brain.node_id or 'openclaw-m1'}",
                        TITLE,
                        brain.cpu_pct, brain.mem_used, brain.mem_total, mac_uptime,
                        extra_rows=[("LLM", _trim(d, brain.model, COL_W-16, F14), CYAN)])
        # row 2 override: uptime from brain.uptime_s
        row_y2 = mac_y + 20
        row_h2 = (NODE_H - 24) // 4
        lbl(d, mac_x+8, row_y2 + 2*row_h2,       "Uptime")
        d.text((mac_x+8, row_y2 + 2*row_h2 + 12), uptime_str(brain.uptime_s), font=F14, fill=FG)
    else:
        panel(d, mac_x, mac_y, COL_W, NODE_H)
        lbl(d, mac_x+8, mac_y+4, f"🧠 {brain.node_id or 'openclaw-m1'}", font=F12, color=TITLE)
        dot(d, mac_x+16, mac_y+44, RED)
        d.text((mac_x+28, mac_y+36), "OFFLINE", font=F18, fill=RED)
        if brain.error:
            err = brain.error[:36]
            lbl(d, mac_x+8, mac_y+64, err, color=YELLOW)

    # ── service bar ───────────────────────────────────────────────────────────
    svc_y  = NODE_Y + NODE_H + PAD
    svc_h  = H - svc_y - FOOTER_H - PAD
    half   = (W - PAD * 3) // 2

    # Brain connection block (left)
    panel(d, PAD, svc_y, half, svc_h)
    lbl(d, PAD+8, svc_y+4, "Brain connection")

    brain_col  = GREEN if brain.online else RED
    dot(d, PAD+14, svc_y+18, brain_col)
    status_txt = "ONLINE" if brain.online else "OFFLINE"
    lbl(d, PAD+24, svc_y+12, status_txt, font=F13, color=brain_col)

    if brain.online:
        lbl(d, PAD+24, svc_y+26, f"ping: {brain.latency_ms:.0f} ms", color=DIM)
        # smart model + latency
        lbl(d, PAD+8, svc_y+40, "smart:", color=DIM)
        smart_short = _trim(d, brain.model, half-100, F11)
        lbl(d, PAD+46, svc_y+40, smart_short, color=CYAN)
        if brain.smart_latency_ms > 0:
            lbl(d, half-50, svc_y+40, f"{brain.smart_latency_ms:.0f}ms", color=DIM)
        # fast model + latency
        if brain.fast_model:
            lbl(d, PAD+8, svc_y+53, "fast: ", color=DIM)
            fast_short = _trim(d, brain.fast_model, half-100, F11)
            lbl(d, PAD+46, svc_y+53, fast_short, color=PURPLE)
            if brain.fast_latency_ms > 0:
                lbl(d, half-50, svc_y+53, f"{brain.fast_latency_ms:.0f}ms", color=DIM)

    # Edge services block (right)
    svc2_x = PAD*2 + half
    panel(d, svc2_x, svc_y, half, svc_h)
    lbl(d, svc2_x+8, svc_y+4, "Edge services")

    row_y = svc_y + 16
    row_s = 13  # px per row

    # Agent
    agent_col = GREEN if agent_active else YELLOW
    dot(d, svc2_x+12, row_y+4, agent_col, r=3)
    lbl(d, svc2_x+22, row_y, "agent", color=DIM)
    lbl(d, svc2_x+22+d.textlength("agent  ", font=F11), row_y,
        "active" if agent_active else "inactive", color=agent_col)
    row_y += row_s

    # Pi services
    for (name, status, ok) in (pi_services or []):
        col = GREEN if ok else (YELLOW if status == "partial" else RED)
        dot(d, svc2_x+12, row_y+4, col, r=3)
        lbl(d, svc2_x+22, row_y, name, color=DIM)
        lbl(d, svc2_x+22+d.textlength(name+"  ", font=F11), row_y, status, color=col)
        row_y += row_s

    # Disk — after services
    disk = local.get("disk", "")
    if disk:
        row_y += 2
        lbl(d, svc2_x+8, row_y, _trim(d, "disk: " + disk, half-16, F10), color=DIM, font=F10)

    # ── footer ────────────────────────────────────────────────────────────────
    d.line([(0, H - FOOTER_H), (W, H - FOOTER_H)], fill=BORDER, width=1)
    lbl(d, 8, H-14, local.get("hostname", OPENLIGHT_NODE_ID), color=DIM)
    upd = "updated " + datetime.now().strftime("%H:%M:%S")
    uw  = d.textlength(upd, font=F10)
    lbl(d, W - uw - 6, H-14, upd, font=F10, color=DIM)

    return img


def read_active_message():
    """Return (text, seconds_left) if an active overlay message exists, else (None, 0)."""
    try:
        with open(MSG_FILE) as f:
            m = json.load(f)
        left = int(m["expires_at"]) - int(time.time())
        if left <= 0:
            os.remove(MSG_FILE)
            return None, 0
        return m["text"], left
    except Exception:
        return None, 0


def build_message_overlay(text, seconds_left):
    """Full-screen overlay with the custom message."""
    img = Image.new("RGB", (W, H), (10, 10, 25))
    d   = ImageDraw.Draw(img)

    # decorative border
    d.rounded_rectangle([6, 6, W-6, H-6], radius=10, outline=PURPLE, width=2)

    # countdown badge top-right
    mins = seconds_left // 60
    secs = seconds_left % 60
    badge = f"{mins}:{secs:02d}"
    bw = d.textlength(badge, font=F13)
    d.text((W - bw - 16, 14), badge, font=F13, fill=DIM)

    # wrap and centre text
    lines = []
    for para in text.splitlines():
        lines.extend(textwrap.wrap(para or " ", width=28) or [""])

    # pick font size based on total lines
    font = F24 if len(lines) <= 4 else F18 if len(lines) <= 7 else F14
    line_h = font.size + 4
    total_h = len(lines) * line_h
    y = max(HEADER_H + 10, (H - total_h) // 2)

    for line in lines:
        lw = d.textlength(line, font=font)
        d.text(((W - lw) // 2, y), line, font=font, fill=FG)
        y += line_h

    # footer hint
    lbl(d, 0, H-14, " tap to dismiss · auto-clear in " + badge, font=F10, color=DIM)

    return img


def build_processing():
    img = Image.new("RGB", (W, H), BG)
    d   = ImageDraw.Draw(img)
    d.rectangle([0, 0, W, HEADER_H], fill=(14, 17, 38))
    d.text((8, 5), "openLight", font=F18, fill=TITLE)
    ts = datetime.now().strftime("%H:%M:%S")
    tw = d.textlength(ts, font=F13)
    d.text((W-tw-6, 7), ts, font=F13, fill=DIM)
    d.line([(0, HEADER_H), (W, HEADER_H)], fill=BORDER, width=1)
    d.text((W//2 - 70, H//2 - 16), "PROCESSING…", font=F32, fill=YELLOW)
    return img


def build_offline(last_ok):
    img = Image.new("RGB", (W, H), BG)
    d   = ImageDraw.Draw(img)
    d.rectangle([0, 0, W, HEADER_H], fill=(14, 17, 38))
    d.text((8, 5), "openLight", font=F18, fill=RED)
    d.line([(0, HEADER_H), (W, HEADER_H)], fill=BORDER, width=1)
    d.text((W//2 - 60, 80), "OFFLINE", font=F32, fill=RED)
    d.text((W//2 - 100, 140), "Last successful update:", font=F14, fill=DIM)
    ts = last_ok.strftime("%Y-%m-%d  %H:%M:%S") if last_ok else "never"
    tw = d.textlength(ts, font=F18)
    d.text((W//2 - tw//2, 162), ts, font=F18, fill=YELLOW)
    return img


# ── main loop ─────────────────────────────────────────────────────────────────

def main():
    last_ok     = None
    last_local  = {}
    last_brain  = BrainInfo.__new__(BrainInfo)
    last_brain.online = False
    last_brain.node_id = ""
    last_brain.model = ""
    last_brain.latency_ms = 0.0
    last_brain.cpu_pct = 0.0
    last_brain.mem_used = 0.0
    last_brain.mem_total = 0.0
    last_brain.uptime_s = 0
    last_brain.error = "not yet polled"
    last_agent_active = False
    last_pi_services  = []

    # SIGUSR1 → immediate redraw (used by display_message skill)
    redraw_flag = [False]
    def _on_sigusr1(signum, frame):
        redraw_flag[0] = True
    signal.signal(signal.SIGUSR1, _on_sigusr1)

    img = Image.new("RGB", (W, H), BG)
    d   = ImageDraw.Draw(img)
    d.text((8, 5),  "openLight", font=F18, fill=TITLE)
    d.text((8, 60), "Starting…", font=F24, fill=DIM)
    push(img)

    next_full_refresh = 0.0

    while True:
        now = time.monotonic()

        # check overlay first — show immediately on SIGUSR1 or on regular tick
        msg_text, msg_left = read_active_message()
        if msg_text:
            push(build_message_overlay(msg_text, msg_left))
            redraw_flag[0] = False
            # sleep in short increments so countdown stays fresh and SIGUSR1 is noticed
            for _ in range(60):
                time.sleep(1)
                redraw_flag[0] = False
                msg_text, msg_left = read_active_message()
                if not msg_text:
                    break
                push(build_message_overlay(msg_text, msg_left))
            # after overlay clears, force a full refresh
            next_full_refresh = 0.0
            continue

        if redraw_flag[0] or now >= next_full_refresh:
            redraw_flag[0] = False
            push(build_processing())

            local        = fetch_local_status()
            brain        = BrainInfo(OPENLIGHT_BRAIN_URL)
            agent_active = fetch_agent_active()
            pi_services  = fetch_pi_services()

            if local:
                last_local       = local
                last_ok          = datetime.now()
            last_brain        = brain
            last_agent_active = agent_active
            last_pi_services  = pi_services

            if last_local:
                push(build_status(last_local, last_brain, last_agent_active, last_pi_services))
            else:
                push(build_offline(last_ok))

            next_full_refresh = time.monotonic() + REFRESH_SEC

        time.sleep(1)


if __name__ == "__main__":
    main()
