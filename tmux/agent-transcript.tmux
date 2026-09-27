# Source after setting preferences. Requires the built CLI and selected reader.
set -gF @agent_transcript_command "#{d:current_file}/../bin/agent-transcript"
set -goq @agent_transcript_reader "leaf"
set -goq @agent_transcript_position "right"
set -goq @agent_transcript_size "50%"
set -goq @agent_transcript_focus "on"
set -goq @agent_transcript_key "P"
set -goq @agent_transcript_copy_mode_key "P"

run-shell "#{q:@agent_transcript_command} _configure"
