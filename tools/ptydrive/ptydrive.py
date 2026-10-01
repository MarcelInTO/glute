#!/usr/bin/env python3
"""Play a scripted scenario against glute running in a real pseudo-terminal.

Unit tests drive the dashboard by calling onKey/onMouse directly, which skips
tview's own event dispatch: how a click is built from a press and a release
(and a move ahead of them, sharing one event), which widget a key actually
reaches, what a real terminal is sent. This runs the built binary in a pty,
feeds it real key and SGR-mouse bytes, and reads the screen back through a
terminal emulator (pyte), so a scenario checks what a user would see.

    ptydrive.py [-q] SCENARIO [-- COMMAND [ARG...]]

COMMAND defaults to `bin/glute --sample`. Use `make drive` / `make drive-all`,
which build the binary and provide the pyte venv. Unix only (it needs a pty).

A scenario is one step per line. `#` starts a comment, except on expect
lines, where it's literal (pipeline ids are written #97). Rows and columns are
1-based, as a terminal counts them. Setup steps come before the first action:

    size 120x30          screen size (the default)
    env NAME=VALUE       set a variable for glute
    unset NAME           remove one (e.g. SSH_CONNECTION, to pin the desktop case)
    opener               put a stand-in `xdg-open`/`open` first on PATH that
                         records each URL it's handed (and prints noise on stdout
                         and stderr, which must not reach the screen)

Actions and checks (glute starts at the first one):

    wait SECONDS         let it run
    settle SECONDS       how long each key/click/wheel waits after (default 0.6)
    key NAME...          press keys: a single character, or enter esc tab backtab
                         up down left right pgup pgdn home end, or ctrl-a … ctrl-z
    click COL ROW        left click (press and release)
    wheel COL ROW up|down
    show [FROM [TO]]     print screen rows (all by default; silent with -q)
    expect TEXT          TEXT is on the screen
    expect-not TEXT      TEXT isn't
    expect-row ROW TEXT  TEXT is on that row
    expect-raw TEXT      glute has written TEXT to the terminal (Python escapes
                         such as \\x1b allowed) — e.g. an OSC 8 hyperlink
    expect-opened TEXT   the stand-in opener was handed a URL containing TEXT
    expect-not-opened    it was handed nothing
    expect-exit          glute has exited (waits up to 2s)

The first failed check stops the scenario, prints the screen, and exits 1.
"""

import fcntl
import os
import pty
import select
import shlex
import signal
import struct
import sys
import tempfile
import termios
import time

try:
    import pyte
except ImportError:
    sys.exit("ptydrive: needs pyte (run it via `make drive`, which sets up the venv)")

KEYS = {
    "enter": "\r", "esc": "\x1b", "tab": "\t", "backtab": "\x1b[Z",
    "up": "\x1b[A", "down": "\x1b[B", "right": "\x1b[C", "left": "\x1b[D",
    "pgup": "\x1b[5~", "pgdn": "\x1b[6~", "home": "\x1b[H", "end": "\x1b[F",
}
KEYS.update({f"ctrl-{c}": chr(ord(c) - 0x60) for c in "abcdefghijklmnopqrstuvwxyz"})

SETUP = {"size", "env", "unset", "opener"}


class Failed(Exception):
    pass


class Drive:
    def __init__(self, command, quiet):
        self.command, self.quiet = command, quiet
        self.cols, self.rows = 120, 30
        self.env = dict(os.environ, TERM="xterm-256color")
        self.settle = 0.6
        self.opened_log = None
        self.pid = None
        self.checks = 0
        self.exited = False

    # --- setup ---------------------------------------------------------------

    def install_opener(self):
        d = tempfile.mkdtemp(prefix="ptydrive-")
        self.opened_log = os.path.join(d, "opened.log")
        open(self.opened_log, "w").close()
        for name in ("xdg-open", "open"):
            path = os.path.join(d, name)
            with open(path, "w") as f:
                f.write("#!/bin/sh\n")
                f.write(f"printf '%s\\n' \"$@\" >> {shlex.quote(self.opened_log)}\n")
                f.write("echo 'stand-in opener chatter'; echo 'stand-in opener stderr' >&2\n")
            os.chmod(path, 0o755)
        self.env["PATH"] = d + os.pathsep + self.env.get("PATH", "")

    def start(self):
        self.screen = pyte.Screen(self.cols, self.rows)
        self.stream = pyte.ByteStream(self.screen)
        self.raw = bytearray()
        self.pid, self.fd = pty.fork()
        if self.pid == 0:
            os.execvpe(self.command[0], self.command, self.env)
        fcntl.ioctl(self.fd, termios.TIOCSWINSZ, struct.pack("HHHH", self.rows, self.cols, 0, 0))
        self.pump(1.5)  # the first snapshot lands and draws

    # --- the terminal ----------------------------------------------------------

    def pump(self, secs):
        end = time.time() + secs
        while time.time() < end:
            if select.select([self.fd], [], [], 0.05)[0]:
                try:
                    b = os.read(self.fd, 65536)
                except OSError:  # glute exited and the pty closed
                    self.exited = True
                    return
                if not b:
                    self.exited = True
                    return
                self.raw.extend(b)
                self.stream.feed(b)

    def send(self, data):
        os.write(self.fd, data.encode())
        self.pump(self.settle)

    def display(self):
        return [line.rstrip() for line in self.screen.display]

    def opened(self):
        with open(self.opened_log) as f:
            return [line.strip() for line in f if line.strip()]

    # --- steps ------------------------------------------------------------------

    def check(self, ok, what):
        self.checks += 1
        if not ok:
            raise Failed(what)

    def step(self, verb, args):
        if verb in SETUP and self.pid is not None:
            raise Failed(f"`{verb}` must come before the first action")
        if verb == "size":
            self.cols, self.rows = (int(n) for n in args[0].split("x"))
        elif verb == "env":
            name, _, value = " ".join(args).partition("=")
            self.env[name] = value
        elif verb == "unset":
            for name in args:
                self.env.pop(name, None)
        elif verb == "opener":
            self.install_opener()
        else:
            if self.pid is None:
                self.start()
            self.action(verb, args)

    def action(self, verb, args):
        text = " ".join(args)
        if verb == "wait":
            self.pump(float(args[0]))
        elif verb == "settle":
            self.settle = float(args[0])
        elif verb == "key":
            for k in args:
                if k in KEYS:
                    self.send(KEYS[k])
                elif len(k) == 1:
                    self.send(k)
                else:
                    raise Failed(f"unknown key {k!r}")
        elif verb == "click":
            col, row = int(args[0]), int(args[1])
            self.send(f"\x1b[<0;{col};{row}M\x1b[<0;{col};{row}m")
        elif verb == "wheel":
            col, row, way = int(args[0]), int(args[1]), args[2]
            self.send(f"\x1b[<{64 if way == 'up' else 65};{col};{row}M")
        elif verb == "show":
            lo = int(args[0]) if args else 1
            hi = int(args[1]) if len(args) > 1 else (lo if args else self.rows)
            if not self.quiet:
                print("\n".join(self.display()[lo - 1:hi]))
        elif verb == "expect":
            self.check(any(text in line for line in self.display()), f"expected {text!r} on screen")
        elif verb == "expect-not":
            self.check(not any(text in line for line in self.display()), f"didn't expect {text!r} on screen")
        elif verb == "expect-row":
            row, want = int(args[0]), " ".join(args[1:])
            self.check(want in self.display()[row - 1], f"expected {want!r} on row {row}")
        elif verb == "expect-raw":
            want = text.encode().decode("unicode_escape").encode("latin-1")
            self.check(want in self.raw, f"expected {text!r} in glute's output to the terminal")
        elif verb in ("expect-opened", "expect-not-opened"):
            if self.opened_log is None:
                raise Failed(f"`{verb}` needs the `opener` setup step")
            got = self.opened()
            if verb == "expect-opened":
                self.check(any(text in u for u in got), f"expected the opener to get a URL with {text!r}; it got {got}")
            else:
                self.check(not got, f"expected no URL opened; the opener got {got}")
        elif verb == "expect-exit":
            for _ in range(40):
                if self.exited or os.waitpid(self.pid, os.WNOHANG)[0] != 0:
                    self.exited = True
                    break
                self.pump(0.05)
            self.check(self.exited, "expected glute to have exited")
        else:
            raise Failed(f"unknown step {verb!r}")

    def stop(self):
        if self.pid and not self.exited:
            try:
                os.kill(self.pid, signal.SIGTERM)
                os.waitpid(self.pid, 0)
            except (ProcessLookupError, ChildProcessError):
                pass


def main(argv):
    quiet = False
    if argv and argv[0] == "-q":
        quiet, argv = True, argv[1:]
    if not argv:
        sys.exit(__doc__)
    scenario, rest = argv[0], argv[1:]
    command = rest[1:] if rest[:1] == ["--"] else ["bin/glute", "--sample"]
    name = os.path.basename(scenario)

    d = Drive(command, quiet)
    try:
        with open(scenario) as f:
            for n, line in enumerate(f, 1):
                line = line.strip()
                if not line.startswith("expect"):
                    line = line.split("#", 1)[0].strip()
                if not line:
                    continue
                verb, *args = line.split()
                try:
                    d.step(verb, args)
                except Failed as e:
                    print(f"FAIL {name}:{n}: {e}")
                    if d.pid is not None:
                        print("\n".join(d.display()))
                    return 1
    finally:
        d.stop()
    print(f"ok   {name} ({d.checks} checks)")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
