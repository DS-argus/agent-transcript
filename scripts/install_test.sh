#!/bin/sh
# Offline integration tests for scripts/install.sh.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
TEST_DIR=$(mktemp -d "${TMPDIR:-/tmp}/agent-transcript-install-test.XXXXXX")
ORIGINAL_PATH=$PATH
DIGEST=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
FAKE_BIN=$TEST_DIR/fake-bin
MIN_BIN=$TEST_DIR/min-bin
REPO="$TEST_DIR/plugin checkout"
LOG=$TEST_DIR/download.log

cleanup() {
    status=$?
    trap - EXIT HUP INT TERM
    rm -rf "$TEST_DIR"
    exit "$status"
}
trap 'cleanup' EXIT HUP INT TERM

fail() {
    printf 'install tests: %s\n' "$*" >&2
    exit 1
}

assert_equal() {
    expected=$1
    actual=$2
    message=$3
    [ "$expected" = "$actual" ] || fail "$message (expected '$expected', got '$actual')"
}

assert_file_version() {
    file=$1
    expected=$2
    [ -x "$file" ] || fail "expected executable $file"
    actual=$("$file" --version 2>/dev/null) || fail "$file --version failed"
    assert_equal "agent-transcript $expected" "$actual" "unexpected installed version"
}

run_failure() {
    if env FAKE_MODE="${FAKE_MODE:-ok}" \
        FAKE_BINARY_VERSION="${FAKE_BINARY_VERSION:-}" \
        FAKE_UNAME_S="${FAKE_UNAME_S:-Darwin}" \
        FAKE_UNAME_M="${FAKE_UNAME_M:-arm64}" \
        "$REPO/scripts/install.sh" >"$TEST_DIR/stdout" 2>"$TEST_DIR/stderr"; then
        fail 'expected installer failure'
    fi
}

reset_log() {
    : > "$LOG"
}

count_downloads() {
    wc -l < "$LOG" | tr -d ' '
}

mkdir -p "$FAKE_BIN" "$MIN_BIN" "$REPO/scripts" "$REPO/bin"
cp "$ROOT/scripts/install.sh" "$REPO/scripts/install.sh"
printf '0.1.0\n' > "$REPO/VERSION"
chmod 755 "$REPO/scripts/install.sh"

cat > "$FAKE_BIN/uname" <<'EOF_UNAME'
#!/bin/sh
case "$1" in
    -s) printf '%s\n' "${FAKE_UNAME_S:-Darwin}" ;;
    -m) printf '%s\n' "${FAKE_UNAME_M:-arm64}" ;;
    *) exit 1 ;;
esac
EOF_UNAME

cat > "$FAKE_BIN/sha256sum" <<'EOF_SHA256'
#!/bin/sh
printf '%s  %s\n' "${FAKE_DIGEST:?}" "$1"
EOF_SHA256

cat > "$FAKE_BIN/curl" <<'EOF_CURL'
#!/bin/sh
output=
url=
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o|--output) output=$2; shift 2 ;;
        http://*|https://*) url=$1; shift ;;
        *) shift ;;
    esac
done
[ -n "$output" ] && [ -n "$url" ] || exit 2
printf '%s\n' "$url" >> "${FAKE_LOG:?}"
if [ "${FAKE_MODE:-ok}" = fail ] || [ "${FAKE_MODE:-ok}" = interrupted ]; then
    if [ "${FAKE_MODE:-ok}" = interrupted ]; then
        printf '%s\n' partial > "$output"
    fi
    exit 22
fi
case "$url" in
    */checksums.txt)
        case "${FAKE_MODE:-ok}" in
            missing) printf '%s  agent-transcript-linux-amd64\n' "${FAKE_DIGEST:?}" > "$output" ;;
            duplicate) printf '%s  agent-transcript-darwin-arm64\n%s  agent-transcript-darwin-arm64\n' "${FAKE_DIGEST:?}" "${FAKE_DIGEST:?}" > "$output" ;;
            mismatch) printf '%064d  agent-transcript-darwin-arm64\n' 0 > "$output" ;;
            invalid) printf 'not-a-digest  agent-transcript-darwin-arm64\n' > "$output" ;;
            *)
                for asset in agent-transcript-darwin-arm64 agent-transcript-darwin-amd64 agent-transcript-linux-arm64 agent-transcript-linux-amd64; do
                    printf '%s  %s\n' "${FAKE_DIGEST:?}" "$asset"
                done > "$output"
                ;;
        esac
        ;;
    *)
        printf '#!/bin/sh\nprintf "agent-transcript %%s\\n" "%s"\n' "${FAKE_BINARY_VERSION:-0.1.0}" > "$output"
        if [ "${FAKE_MODE:-ok}" = slow ]; then sleep 1; fi
        ;;
esac
exit 0
EOF_CURL

# The wget fake uses the same release behavior while accepting wget's -O flag.
sed 's/-o|--output)/-o|--output|-O)/' "$FAKE_BIN/curl" > "$FAKE_BIN/wget"
chmod 755 "$FAKE_BIN/uname" "$FAKE_BIN/sha256sum" "$FAKE_BIN/curl" "$FAKE_BIN/wget"

# A small PATH for tool-absence tests.  It has the shell utilities the
# installer needs, but deliberately has no downloader or checksum command.
for tool in dirname mkdir mktemp rm rmdir sleep mv chmod; do
    ln -s "$(command -v "$tool")" "$MIN_BIN/$tool"
done
ln -s "$FAKE_BIN/uname" "$MIN_BIN/uname"

export PATH=$FAKE_BIN:$ORIGINAL_PATH
export FAKE_LOG=$LOG FAKE_DIGEST=$DIGEST
export FAKE_UNAME_S=Darwin FAKE_UNAME_M=arm64

# A successful install in a checkout whose path contains spaces.
reset_log
FAKE_MODE=ok FAKE_BINARY_VERSION=0.1.0 "$REPO/scripts/install.sh"
assert_file_version "$REPO/bin/agent-transcript" 0.1.0
assert_equal 2 "$(count_downloads)" 'first install should download binary and checksums'
case "$(sed -n '1p' "$LOG")" in
    https://github.com/DS-argus/agent-transcript/releases/download/v0.1.0/agent-transcript-darwin-arm64) ;;
    *) fail 'binary URL did not use the exact version tag and platform asset' ;;
esac

# A matching executable is a cache hit and must not invoke the downloader.
reset_log
FAKE_MODE=fail FAKE_BINARY_VERSION=0.1.0 "$REPO/scripts/install.sh"
assert_equal 0 "$(count_downloads)" 'matching cached binary should avoid network access'

# Updating VERSION installs the new release.
printf '0.2.0\n' > "$REPO/VERSION"
reset_log
FAKE_MODE=ok FAKE_BINARY_VERSION=0.2.0 "$REPO/scripts/install.sh"
assert_file_version "$REPO/bin/agent-transcript" 0.2.0
assert_equal 2 "$(count_downloads)" 'version update should download a release'

# A bad downloaded binary and a checksum mismatch both leave the old file in place.
printf '0.3.0\n' > "$REPO/VERSION"
FAKE_MODE=ok FAKE_BINARY_VERSION=0.9.0 run_failure
assert_file_version "$REPO/bin/agent-transcript" 0.2.0
FAKE_MODE=mismatch FAKE_BINARY_VERSION=0.3.0 run_failure
assert_file_version "$REPO/bin/agent-transcript" 0.2.0

# Missing and duplicate checksum entries are rejected without replacement.
FAKE_MODE=missing FAKE_BINARY_VERSION=0.3.0 run_failure
assert_file_version "$REPO/bin/agent-transcript" 0.2.0
FAKE_MODE=duplicate FAKE_BINARY_VERSION=0.3.0 run_failure
assert_file_version "$REPO/bin/agent-transcript" 0.2.0
FAKE_MODE=interrupted FAKE_BINARY_VERSION=0.3.0 run_failure
assert_file_version "$REPO/bin/agent-transcript" 0.2.0
[ ! -e "$REPO/bin/.agent-transcript.lock" ] || fail 'installer lock leaked after a failed download'

# Unsupported platforms are rejected before a downloader is called.
printf '0.4.0\n' > "$REPO/VERSION"
reset_log
FAKE_UNAME_S=FreeBSD FAKE_UNAME_M=arm64 FAKE_MODE=fail run_failure
assert_equal 0 "$(count_downloads)" 'unsupported OS should be rejected before download'
export FAKE_UNAME_S=Darwin FAKE_UNAME_M=arm64

# Missing downloader and checksum tools produce actionable failures.
FRESH=$TEST_DIR/fresh
mkdir -p "$FRESH/scripts" "$FRESH/bin"
cp "$ROOT/scripts/install.sh" "$FRESH/scripts/install.sh"
printf '0.1.0\n' > "$FRESH/VERSION"
chmod 755 "$FRESH/scripts/install.sh"
if PATH=$MIN_BIN "$FRESH/scripts/install.sh" >"$TEST_DIR/stdout" 2>"$TEST_DIR/stderr"; then
    fail 'missing downloader should fail'
fi
case "$(cat "$TEST_DIR/stderr")" in
    *'curl or wget is required'*) ;;
    *) fail 'missing downloader error was not actionable' ;;
esac

# Build a PATH with fake curl but no checksum utility.
NO_SUM=$TEST_DIR/no-sum
mkdir -p "$NO_SUM"
for tool in dirname mkdir mktemp rm rmdir sleep mv chmod; do
    ln -s "$(command -v "$tool")" "$NO_SUM/$tool"
done
ln -s "$FAKE_BIN/uname" "$NO_SUM/uname"
ln -s "$FAKE_BIN/curl" "$NO_SUM/curl"
if PATH=$NO_SUM FAKE_MODE=ok FAKE_BINARY_VERSION=0.1.0 "$FRESH/scripts/install.sh" >"$TEST_DIR/stdout" 2>"$TEST_DIR/stderr"; then
    fail 'missing checksum tool should fail'
fi
case "$(cat "$TEST_DIR/stderr")" in
    *'sha256sum or shasum is required'*) ;;
    *) fail 'missing checksum error was not actionable' ;;
esac

# Concurrent installers serialize through mkdir locking; only one fetches.
CONCURRENT=$TEST_DIR/concurrent
mkdir -p "$CONCURRENT/scripts" "$CONCURRENT/bin"
cp "$ROOT/scripts/install.sh" "$CONCURRENT/scripts/install.sh"
printf '0.5.0\n' > "$CONCURRENT/VERSION"
chmod 755 "$CONCURRENT/scripts/install.sh"
reset_log
FAKE_MODE=slow FAKE_BINARY_VERSION=0.5.0 "$CONCURRENT/scripts/install.sh" >"$TEST_DIR/one.out" 2>"$TEST_DIR/one.err" &
pid_one=$!
FAKE_MODE=slow FAKE_BINARY_VERSION=0.5.0 "$CONCURRENT/scripts/install.sh" >"$TEST_DIR/two.out" 2>"$TEST_DIR/two.err" &
pid_two=$!
wait "$pid_one"
wait "$pid_two"
assert_file_version "$CONCURRENT/bin/agent-transcript" 0.5.0
assert_equal 2 "$(count_downloads)" 'concurrent installers should perform one binary/checksum download pair'

# All supported platform spellings download exactly one matching binary.
for spec in Darwin:arm64:darwin-arm64 Darwin:x86_64:darwin-amd64 Linux:aarch64:linux-arm64 Linux:amd64:linux-amd64; do
    old_ifs=$IFS; IFS=:; set -- $spec; IFS=$old_ifs
    rm -f "$REPO/bin/agent-transcript"
    printf '0.1.0\n' > "$REPO/VERSION"
    reset_log
    FAKE_UNAME_S=$1 FAKE_UNAME_M=$2 FAKE_MODE=ok FAKE_BINARY_VERSION=0.1.0 "$REPO/scripts/install.sh"
    assert_equal "https://github.com/DS-argus/agent-transcript/releases/download/v0.1.0/agent-transcript-$3" "$(sed -n '1p' "$LOG")" 'wrong platform asset'
    assert_equal 2 "$(count_downloads)" 'downloaded extra platform assets'
done

for value in '' 01.2.3 1.2 1.2.3.4 1.2.3-beta '1; echo bad'; do
    printf '%s\n' "$value" > "$REPO/VERSION"
    reset_log
    run_failure
    assert_equal 0 "$(count_downloads)" 'invalid VERSION performed a download'
done
printf '0.1.0\n' > "$REPO/VERSION"
reset_log
FAKE_UNAME_M=riscv64 run_failure
assert_equal 0 "$(count_downloads)" 'unsupported CPU performed a download'
export FAKE_UNAME_S=Darwin FAKE_UNAME_M=arm64

# Isolate PATH so wget/shasum are used even if host curl/sha256sum exist.
FALLBACK=$TEST_DIR/fallback-bin
mkdir -p "$FALLBACK"
for tool in dirname mkdir mktemp rm rmdir sleep mv chmod tr; do
    ln -s "$(command -v "$tool")" "$FALLBACK/$tool"
done
ln -s "$FAKE_BIN/uname" "$FALLBACK/uname"
ln -s "$FAKE_BIN/wget" "$FALLBACK/wget"
printf '#!/bin/sh\nprintf "%%s  %%s\\n" "$FAKE_DIGEST" "$3"\n' > "$FALLBACK/shasum"
chmod 755 "$FALLBACK/shasum"
rm -f "$REPO/bin/agent-transcript"
reset_log
PATH=$FALLBACK FAKE_MODE=ok FAKE_BINARY_VERSION=0.1.0 "$REPO/scripts/install.sh"
assert_file_version "$REPO/bin/agent-transcript" 0.1.0
assert_equal 2 "$(count_downloads)" 'wget fallback failed'
printf '%s\n' 'install tests: PASS'
