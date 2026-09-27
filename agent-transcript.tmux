#!/bin/sh
# TPM loads this file; install the matching release before sourcing tmux defaults.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)

if "$ROOT/scripts/install.sh"; then
    :
else
    status=$?
    printf 'agent-transcript: unable to install the required binary; tmux configuration was not loaded.\n' >&2
    exit "$status"
fi

tmux source-file "$ROOT/tmux/agent-transcript.tmux"
