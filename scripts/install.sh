#!/bin/sh
# Install the release binary that matches this checkout and host.
set -eu

# Resolve the checkout from this script rather than from the caller's cwd.
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P) || {
    printf '%s\n' 'agent-transcript: cannot determine the installer directory.' >&2
    exit 1
}
ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd -P) || {
    printf '%s\n' 'agent-transcript: cannot determine the plugin directory.' >&2
    exit 1
}
BIN_DIR=$ROOT/bin
BINARY=$BIN_DIR/agent-transcript
LOCK_DIR=$BIN_DIR/.agent-transcript.lock
LOCK_WAIT_ATTEMPTS=30

TMP_BINARY=
TMP_CHECKSUMS=
LOCK_OWNED=0
LOCK_TOKEN=

cleanup() {
    status=$1
    trap - EXIT HUP INT TERM

    if [ -n "$TMP_BINARY" ]; then
        rm -f "$TMP_BINARY" 2>/dev/null || :
    fi
    if [ -n "$TMP_CHECKSUMS" ]; then
        rm -f "$TMP_CHECKSUMS" 2>/dev/null || :
    fi

    # Only the process that created the directory may remove it.  Checking the
    # owner marker avoids deleting a lock acquired by another process.
    if [ "$LOCK_OWNED" -eq 1 ]; then
        current_token=
        if [ -f "$LOCK_DIR/owner" ]; then
            IFS= read -r current_token < "$LOCK_DIR/owner" || :
        fi
        if [ "$current_token" = "$LOCK_TOKEN" ]; then
            rm -f "$LOCK_DIR/owner" 2>/dev/null || :
            rmdir "$LOCK_DIR" 2>/dev/null || :
        fi
        LOCK_OWNED=0
    fi

    exit "$status"
}
trap 'cleanup "$?"' EXIT
trap 'cleanup 129' HUP
trap 'cleanup 130' INT
trap 'cleanup 143' TERM

die() {
    printf 'agent-transcript: %s\n' "$*" >&2
    exit 1
}

read_version() {
    VERSION_LINE_COUNT=0
    line=
    VERSION_VALUE=
    while IFS= read -r line || [ -n "$line" ]; do
        VERSION_LINE_COUNT=$((VERSION_LINE_COUNT + 1))
        if [ "$VERSION_LINE_COUNT" -eq 1 ]; then
            VERSION_VALUE=$line
        fi
    done < "$ROOT/VERSION" || die "cannot read $ROOT/VERSION"

    if [ "$VERSION_LINE_COUNT" -ne 1 ]; then
        die "VERSION must contain exactly one numeric semver line (for example 0.1.0)."
    fi

    # SemVer numeric identifiers contain digits only, have three components,
    # and do not have leading zeroes (except for the value 0).
    case "$VERSION_VALUE" in
        ''|*[!0-9.]*|.*|*.|*..*)
            die "VERSION must be a numeric semver such as 0.1.0."
            ;;
    esac
    old_ifs=$IFS
    IFS=.
    set -- $VERSION_VALUE
    IFS=$old_ifs
    if [ "$#" -ne 3 ]; then
        die "VERSION must be a numeric semver such as 0.1.0."
    fi
    for component do
        case "$component" in
            ''|0[0-9]*|*[!0-9]*)
                die "VERSION must be a numeric semver such as 0.1.0."
                ;;
        esac
    done
    VERSION=$VERSION_VALUE
}

read_version

if ! command -v uname >/dev/null 2>&1; then
    die 'uname is required to detect the release platform.'
fi
OS_RAW=$(uname -s) || die 'unable to detect the operating system with uname.'
ARCH=$(uname -m) || die 'unable to detect the CPU architecture with uname.'
case "$OS_RAW" in
    Darwin) OS=darwin ;;
    Linux) OS=linux ;;
    *) die "unsupported operating system '$OS_RAW'; supported systems are Darwin and Linux." ;;
esac
case "$ARCH" in
    arm64|aarch64) ARCH=arm64 ;;
    amd64|x86_64) ARCH=amd64 ;;
    *) die "unsupported CPU architecture '$ARCH'; supported architectures are arm64 and amd64." ;;
esac

ASSET_NAME=agent-transcript-$OS-$ARCH
BASE_URL=https://github.com/DS-argus/agent-transcript/releases/download/v$VERSION
BINARY_URL=$BASE_URL/$ASSET_NAME
CHECKSUM_URL=$BASE_URL/checksums.txt
EXPECTED_VERSION="agent-transcript $VERSION"

mkdir -p "$BIN_DIR" || die "cannot create binary directory $BIN_DIR."

acquire_lock() {
    attempt=0
    while ! mkdir "$LOCK_DIR" 2>/dev/null; do
        attempt=$((attempt + 1))
        if [ "$attempt" -ge "$LOCK_WAIT_ATTEMPTS" ]; then
            die "timed out waiting for another installation in $LOCK_DIR."
        fi
        sleep 1
    done

    LOCK_OWNED=1
    LOCK_TOKEN=$$
    if ! printf '%s\n' "$LOCK_TOKEN" > "$LOCK_DIR/owner"; then
        die "cannot record ownership of installation lock $LOCK_DIR."
    fi
}

# Keep the cache check under the lock so concurrent invocations do not both
# download an update.
acquire_lock
if [ -x "$BINARY" ]; then
    existing_version=$("$BINARY" --version 2>/dev/null) || existing_version=
    if [ "$existing_version" = "$EXPECTED_VERSION" ]; then
        exit 0
    fi
fi

if command -v curl >/dev/null 2>&1; then
    DOWNLOADER=curl
elif command -v wget >/dev/null 2>&1; then
    DOWNLOADER=wget
else
    die 'curl or wget is required to download the release binary.'
fi

if command -v sha256sum >/dev/null 2>&1; then
    CHECKSUM_TOOL=sha256sum
elif command -v shasum >/dev/null 2>&1; then
    CHECKSUM_TOOL=shasum
else
    die 'sha256sum or shasum is required to verify the release binary.'
fi

TMP_BINARY=$(mktemp "$BIN_DIR/.agent-transcript-download.XXXXXX") || \
    die "cannot create a temporary download in $BIN_DIR."
TMP_CHECKSUMS=$(mktemp "$BIN_DIR/.agent-transcript-checksums.XXXXXX") || \
    die "cannot create a temporary checksum file in $BIN_DIR."

download() {
    url=$1
    destination=$2
    case "$DOWNLOADER" in
        curl)
            curl --fail --location --silent --show-error \
                --proto '=https' --proto-redir '=https' \
                --connect-timeout 10 --max-time 120 \
                -o "$destination" "$url"
            ;;
        wget)
            wget --https-only --timeout=10 --tries=1 \
                -O "$destination" "$url"
            ;;
    esac
}

if ! download "$BINARY_URL" "$TMP_BINARY"; then
    die "download failed for $BINARY_URL."
fi
if ! download "$CHECKSUM_URL" "$TMP_CHECKSUMS"; then
    die "download failed for $CHECKSUM_URL."
fi

# Parse the checksum list without accepting a suffix or a path that merely
# contains the selected basename.  Binary-mode entries (*name) are accepted.
checksum_match_count=0
checksum_value=
candidate_digest=
candidate_name=
candidate_rest=
checksum_rest=
FIELD_IFS=$(printf ' \t')
while IFS=$FIELD_IFS read -r candidate_digest candidate_name candidate_rest || \
    [ -n "$candidate_digest$candidate_name$candidate_rest" ]; do
    case "$candidate_name" in
        "$ASSET_NAME"|\*"$ASSET_NAME")
            checksum_match_count=$((checksum_match_count + 1))
            if [ "$checksum_match_count" -eq 1 ]; then
                checksum_value=$candidate_digest
                checksum_rest=$candidate_rest
            fi
            ;;
    esac
done < "$TMP_CHECKSUMS"

if [ "$checksum_match_count" -ne 1 ]; then
    die "checksums.txt must contain exactly one entry for $ASSET_NAME (found $checksum_match_count)."
fi
if [ -n "$checksum_rest" ]; then
    die "checksum entry for $ASSET_NAME has an invalid format."
fi
case "$checksum_value" in
    ''|*[!0123456789abcdefABCDEF]*)
        die "checksum entry for $ASSET_NAME is not a 64-character hexadecimal digest."
        ;;
esac
if [ "${#checksum_value}" -ne 64 ]; then
    die "checksum entry for $ASSET_NAME is not a 64-character hexadecimal digest."
fi

calculate_checksum() {
    checksum_output=
    case "$CHECKSUM_TOOL" in
        sha256sum) checksum_output=$(sha256sum "$1") || return 1 ;;
        shasum) checksum_output=$(shasum -a 256 "$1") || return 1 ;;
    esac
    old_ifs=$IFS
    IFS=$FIELD_IFS
    set -- $checksum_output
    IFS=$old_ifs
    [ "$#" -ge 1 ] || return 1
    printf '%s' "$1"
}

if ! actual_checksum=$(calculate_checksum "$TMP_BINARY"); then
    die "cannot calculate the SHA-256 checksum of the downloaded binary."
fi
case "$actual_checksum" in
    ''|*[!0123456789abcdefABCDEF]*)
        die 'checksum tool returned an invalid digest.'
        ;;
esac
if [ "${#actual_checksum}" -ne 64 ]; then
    die 'checksum tool returned an invalid digest.'
fi
checksum_value=$(printf '%s' "$checksum_value" | tr 'ABCDEF' 'abcdef') || die 'cannot normalize the checksum digest.'
actual_checksum=$(printf '%s' "$actual_checksum" | tr 'ABCDEF' 'abcdef') || die 'cannot normalize the calculated digest.'
if [ "$actual_checksum" != "$checksum_value" ]; then
    die "checksum mismatch for $ASSET_NAME."
fi
chmod 755 "$TMP_BINARY" || die 'cannot make the downloaded binary executable.'
downloaded_version=$("$TMP_BINARY" --version 2>/dev/null) || \
    die "downloaded $ASSET_NAME could not be executed with --version."
if [ "$downloaded_version" != "$EXPECTED_VERSION" ]; then
    die "downloaded binary reported '$downloaded_version'; expected '$EXPECTED_VERSION'."
fi

# The temporary file and destination are in the same directory, so this
# replacement is atomic and a failed download never touches an existing binary.
mv -f "$TMP_BINARY" "$BINARY" || die "cannot install the downloaded binary at $BINARY."
TMP_BINARY=
printf 'agent-transcript: installed version %s for %s/%s.\n' "$VERSION" "$OS" "$ARCH"
exit 0
