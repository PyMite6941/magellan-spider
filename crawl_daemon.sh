#!/usr/bin/env bash
# Continuous local crawler. Start it, stop it whenever, start it again — it always
# resumes at the next uncrawled topic and never re-does finished work.
#
#   bash crawl_daemon.sh                 # run until stopped
#   bash crawl_daemon.sh --batches 5     # run five batches then exit
#   bash crawl_daemon.sh --status        # where am I, how much is left
#   bash crawl_daemon.sh --stop          # ask a running instance to finish and quit
#
# Stopping:
#   Ctrl-C            finishes nothing further and exits; the current batch's
#                     progress is lost but nothing is corrupted, and the cursor
#                     only advances after a batch publishes successfully.
#   --stop            graceful: the running loop finishes its current batch,
#                     publishes it, advances the cursor, then exits.
#
# State is a separate file from the CI cursor (.crawl-state), so a local run and
# the GitHub Actions schedule never fight over the same counter. They may cover the
# same topics; that is harmless, because uploads upsert rather than duplicate.

set -uo pipefail
cd "$(dirname "$0")"

STATE_FILE=".crawl-state.local"
STOP_FILE=".crawl-stop"
DAEMON_LOG="crawl-daemon.log"
TOPICS_FILE="topics.txt"

BATCH_SIZE="${BATCH_SIZE:-5}"
LIMIT="${LIMIT:-40}"
PAUSE="${PAUSE:-60}"          # seconds between batches — politeness, not throttling
MAX_BATCHES=0                  # 0 = run until stopped

while [ $# -gt 0 ]; do
    case "$1" in
        --batches) MAX_BATCHES="$2"; shift 2 ;;
        --stop)    touch "$STOP_FILE"; echo "stop requested — the running loop will exit after its current batch"; exit 0 ;;
        --status)
            TOTAL=$(grep -c . "$TOPICS_FILE")
            CUR=0; [ -f "$STATE_FILE" ] && CUR=$(tr -dc '0-9' < "$STATE_FILE")
            [ -z "$CUR" ] && CUR=0
            echo "local cursor : topic $((CUR + 1)) of $TOTAL  ($(( CUR * 100 / TOTAL ))% through this pass)"
            [ -f "$DAEMON_LOG" ] && { echo "last activity:"; tail -3 "$DAEMON_LOG"; }
            pgrep -f "crawl_daemon.sh" >/dev/null 2>&1 && echo "status       : RUNNING" || echo "status       : not running"
            exit 0 ;;
        *) echo "unknown option: $1"; exit 1 ;;
    esac
done

rm -f "$STOP_FILE"

# Windows builds carry the .exe suffix; the Linux CI runner does not. Pick whichever
# exists so the same script runs unchanged in both places.
if [ -x "./magellan-spider.exe" ]; then
    SPIDER_BIN="./magellan-spider.exe"
else
    SPIDER_BIN="./magellan-spider"
fi

RUNNING=1
trap 'echo; echo "interrupted — exiting after the current step"; RUNNING=0' INT TERM

TOTAL=$(grep -c . "$TOPICS_FILE")
echo "crawl daemon starting — $TOTAL topics, batches of $BATCH_SIZE, ${LIMIT} pages each"
echo "stop with:  bash crawl_daemon.sh --stop   (or Ctrl-C)"

BATCH_NUM=0
while [ "$RUNNING" -eq 1 ]; do
    if [ -f "$STOP_FILE" ]; then
        echo "stop file found — exiting cleanly"
        rm -f "$STOP_FILE"
        break
    fi
    if [ "$MAX_BATCHES" -gt 0 ] && [ "$BATCH_NUM" -ge "$MAX_BATCHES" ]; then
        echo "reached --batches $MAX_BATCHES — done"
        break
    fi

    BATCH_NUM=$((BATCH_NUM + 1))
    CUR=0; [ -f "$STATE_FILE" ] && CUR=$(tr -dc '0-9' < "$STATE_FILE")
    [ -z "$CUR" ] && CUR=0
    echo
    echo "───── batch $BATCH_NUM — topics $((CUR + 1))-$((CUR + BATCH_SIZE)) of $TOTAL — $(date +%H:%M:%S) ─────"

    # crawl_batch.sh owns crawling, filtering, uploading and advancing the cursor.
    # Running it per batch is what makes this interruptible: kill the daemon at any
    # point and at worst one unpublished batch is lost.
    if BATCH_SIZE="$BATCH_SIZE" LIMIT="$LIMIT" STATE_FILE="$STATE_FILE" \
       DB="crawl-batch.sq3" LOG="$DAEMON_LOG" SPIDER="$SPIDER_BIN" bash crawl_batch.sh; then
        echo "batch $BATCH_NUM published"
    else
        echo "batch $BATCH_NUM FAILED — cursor not advanced, it will be retried"
        sleep 30
    fi

    NEW=0; [ -f "$STATE_FILE" ] && NEW=$(tr -dc '0-9' < "$STATE_FILE")
    [ -z "$NEW" ] && NEW=0
    if [ "$NEW" -lt "$CUR" ]; then
        echo "wrapped past the end of topics.txt — a full pass is complete"
        echo "continuing: a second pass refreshes the oldest pages first"
    fi

    [ "$RUNNING" -eq 1 ] && [ ! -f "$STOP_FILE" ] && sleep "$PAUSE"
done

CUR=0; [ -f "$STATE_FILE" ] && CUR=$(tr -dc '0-9' < "$STATE_FILE")
echo
echo "stopped after $BATCH_NUM batch(es). Next run resumes at topic $((CUR + 1)) of $TOTAL."
