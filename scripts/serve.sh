#!/bin/bash
# serve.sh runs the Stack Overflow server for a game night.
#
#   scripts/serve.sh [server flags]      for example: scripts/serve.sh -admin pin:alice
#
# It does four things that a plain ./bin/stack-overflow does not do:
#   - It keeps the Mac awake (caffeinate) while the script runs.
#   - It writes the server output, with a time stamp on each line, to the
#     log file and to this terminal.
#   - It starts the server again 2 seconds after a crash (exit code other
#     than 0 or 64). A Go panic exits with code 2, so code 2 is a crash.
#     It stops after 5 crashes in a row that each come less than 60
#     seconds after the start.
#   - Ctrl+C (or SIGTERM or SIGHUP to this script) stops the server
#     cleanly: the server saves the state and tells the players. A closed
#     terminal window or tmux pane (SIGHUP) does not stop that save. Only
#     a second Ctrl+C or SIGTERM stops the server at once, without the save.
#
# Settings (environment variables):
#   STACK_OVERFLOW_BIN   the binary (default: bin/stack-overflow next to this folder)
#   STACK_OVERFLOW_LOG   the log file (default: ~/.stack-overflow/server.log)
#
# Exit codes: 0 after a clean stop, 64 for bad flags, 1 after too many crashes.
# The script works with the bash 3.2 of macOS.

set -u

here="$(cd "$(dirname "$0")/.." && pwd)"
BIN="${STACK_OVERFLOW_BIN:-$here/bin/stack-overflow}"
LOG="${STACK_OVERFLOW_LOG:-$HOME/.stack-overflow/server.log}"
MAX_CRASHES=5   # crashes in a row that stop the script
FAST=60         # seconds: a crash sooner than this after the start is "fast"
MAX_LOG=10485760 # bytes: a bigger log moves to server.log.1 at the start
USAGE=64        # the exit code of the server for bad flags (EX_USAGE)

if [ ! -x "$BIN" ]; then
	echo "serve.sh: there is no binary at $BIN. Run \"make install\" first." >&2
	exit 1
fi
mkdir -p "$(dirname "$LOG")" || exit 1
if [ -f "$LOG" ] && [ "$(wc -c <"$LOG")" -gt "$MAX_LOG" ]; then
	mv -f "$LOG" "$LOG.1"
fi

# out writes one line to the log file and to the terminal. After the first
# failed write to the terminal (for example, the terminal window closed),
# out writes only to the log file. bash keeps the failed line in its output
# buffer, and the next write to the log file would write that line a
# second time. So out writes that buffer to /dev/null.
term=1
out() {
	printf '%s\n' "$1" >>"$LOG"
	if [ "$term" = 1 ] && ! printf '%s\n' "$1" 2>/dev/null; then
		term=0
		printf '\n' >/dev/null 2>&1
	fi
}

# stamp writes each input line with a time stamp to the log file and to the
# terminal. It ignores Ctrl+C, so that it can write the shutdown lines of
# the server. A terminal that is gone does not stop it: the log file still
# gets each line, and the server never writes into a closed pipe.
stamp() {
	trap '' INT TERM HUP
	local line
	while IFS= read -r line; do
		case "$line" in
		[0-9][0-9][0-9][0-9]/[0-9][0-9]/[0-9][0-9]\ *) ;; # a server log line has a time
		*) line="$(date '+%Y/%m/%d %H:%M:%S') $line" ;;
		esac
		out "$line"
	done
}

# note writes one line of the script itself. It runs in the script process,
# not as a job, so that Ctrl+C always reaches the script.
note() {
	out "$(date '+%Y/%m/%d %H:%M:%S') serve.sh: $*"
}

# Job control puts each background job (the server, caffeinate, sleep) in
# its own process group. Ctrl+C then reaches only this script, and the
# script sends one SIGINT to the server (see on_signal). Two signals would
# make the server stop at once, without a save. The script runs no
# foreground job.
set -m

if [ "$(uname)" = Darwin ] && command -v caffeinate >/dev/null 2>&1; then
	# -i: no idle sleep. -m: no disk sleep. -s: no system sleep on AC
	# power. -w: stop when this script stops.
	caffeinate -ims -w $$ &
fi

# on_signal sends one SIGINT to the server for the first signal. The server
# then saves and stops. After that first SIGINT, the server does not catch
# SIGINT any more, so a second SIGINT kills it at once, without the save.
#   - A SIGHUP never sends that second SIGINT. A terminal window or tmux
#     pane that closes sends SIGHUP to the script, often two times, and
#     often right after a Ctrl+C. The save must not stop.
#   - A second Ctrl+C or SIGTERM sends the second SIGINT. This is the
#     forced stop.
stop=0
child=""
on_signal() {
	if [ "$stop" = 1 ]; then
		[ "$1" = HUP ] && return
		if [ -n "$child" ]; then
			note "a second $1: the server stops at once, without the final save."
			kill -INT "$child" 2>/dev/null
		fi
		return
	fi
	stop=1
	if [ -n "$child" ]; then
		kill -INT "$child" 2>/dev/null
	fi
}
trap 'on_signal INT' INT
trap 'on_signal TERM' TERM
trap 'on_signal HUP' HUP

crashes=0
note "log file: $LOG"
while :; do
	start=$(date +%s)
	note "start: $BIN $*"
	"$BIN" "$@" > >(stamp) 2>&1 &
	child=$!
	# wait returns early when a signal comes. Wait again until the server
	# process is gone.
	while :; do
		wait "$child"
		code=$?
		kill -0 "$child" 2>/dev/null || break
	done
	child=""
	sleep 0.2 & # let stamp write the last lines
	wait $!
	if [ "$stop" = 1 ] || [ "$code" = 0 ]; then
		note "the server stopped (exit code $code)."
		exit 0
	fi
	if [ "$code" = "$USAGE" ]; then
		note "the server refused the flags (exit code $USAGE). Fix the flags and start again."
		exit "$USAGE"
	fi
	if [ $(($(date +%s) - start)) -lt "$FAST" ]; then
		crashes=$((crashes + 1))
	else
		crashes=1
	fi
	if [ "$crashes" -ge "$MAX_CRASHES" ]; then
		note "the server stopped $crashes times in a row soon after the start (last exit code $code). serve.sh stops. See $LOG."
		exit 1
	fi
	note "the server stopped with exit code $code. A new start comes in 2 seconds. Press Ctrl+C to cancel."
	# A background sleep keeps this script in the foreground, so Ctrl+C
	# during the pause reaches the trap.
	sleep 2 &
	wait $!
	if [ "$stop" = 1 ]; then
		note "stopped."
		exit 0
	fi
done
