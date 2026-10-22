#!/usr/bin/env bash
# selftest.sh — integration test for the acceptance orchestrator.
#
# The orchestrator is the part of this harness that has never been exercised,
# because exercising it needed Windows and a licensed Office. That left the
# sequence it implements -- start the guest, wait for it, copy the inputs,
# trigger the task in the interactive session, read the exit code, collect the
# diagnostics, tear the guest down -- entirely unverified, including which
# exit code it reports. A wrong exit code there is the worst failure this
# harness can have: it is the difference between "PowerPoint refused the file"
# and "we never asked PowerPoint".
#
# So the guest is faked. `podman`, `ssh`, `scp`, `qemu-img` and
# `qemu-system-x86_64` are replaced with scripts that log every invocation and
# answer the way a real guest would, and the orchestrator is run against them
# for real. What is tested is the orchestration; what is not is Windows, which
# no amount of mocking can stand in for.
set -uo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
SCRIPT="$REPO_ROOT/scripts/powerpoint-acceptance.sh"

PASS=0
FAIL=0

ok()   { printf '  ok    %s\n' "$*"; PASS=$((PASS + 1)); }
bad()  { printf '  FAIL  %s\n' "$*"; FAIL=$((FAIL + 1)); }
check() {
  local what="$1" want="$2" got="$3"
  if [[ "$want" == "$got" ]]; then ok "$what"; else bad "$what (want $want, got $got)"; fi
}
contains() {
  local what="$1" needle="$2" file="$3"
  if grep -qF -- "$needle" "$file"; then ok "$what"; else bad "$what — no '$needle' in $file"; fi
}
absent() {
  local what="$1" needle="$2" file="$3"
  if grep -qF -- "$needle" "$file"; then bad "$what — unexpected '$needle'"; else ok "$what"; fi
}

# ── the fake guest ──────────────────────────────────────────────────────────

# GUEST_EXIT is what the faked guest reports from its test run; CALLS is where
# every faked command appends what it was asked to do.
make_fakes() {
  local dir="$1" guest_exit="$2"
  mkdir -p "$dir"

  cat >"$dir/ssh" <<'FAKE'
#!/usr/bin/env bash
echo "ssh $*" >>"$CALLS"
cmd="${*: -1}"
case "$cmd" in
  *"echo ready"*)  echo ready ;;
  *"query session"*) echo " console           tester                    1  Active" ;;
  *exitcode*)
      # First poll finds nothing, as a real run would while the test is still
      # going; the second finds the result. A harness that only ever saw an
      # immediate answer would not be testing the wait at all.
      if [[ -f "$CALLS.polled" ]]; then echo "$GUEST_EXIT"; else : >"$CALLS.polled"; fi ;;
  *schtasks*)      ;;
  *shutdown*)      ;;
  *)               ;;
esac
FAKE

  cat >"$dir/scp" <<'FAKE'
#!/usr/bin/env bash
echo "scp $*" >>"$CALLS"
# Collecting the guest's artifacts has to leave something behind, or the
# orchestrator's own reporting of where to look would be untested.
dest="${*: -1}"
case "$dest" in
  */guest) mkdir -p "$dest" && echo "fake pytest log" >"$dest/pytest.log" ;;
esac
FAKE

  cat >"$dir/podman" <<'FAKE'
#!/usr/bin/env bash
echo "podman $*" >>"$CALLS"
case "$1" in
  run)     echo "deadbeefcafe" ;;
  inspect) echo true ;;
  logs)    echo "fake container log" ;;
  rm)      ;;
esac
FAKE

  cat >"$dir/qemu-img" <<'FAKE'
#!/usr/bin/env bash
echo "qemu-img $*" >>"$CALLS"
case "$2" in
  -l) echo "1  clean-provisioned  0 B  2026-01-01 00:00:00" ;;
esac
FAKE

  cat >"$dir/qemu-system-x86_64" <<'FAKE'
#!/usr/bin/env bash
echo "qemu $*" >>"$CALLS"
# -daemonize means a real qemu returns immediately and the VM keeps running,
# so the pidfile must name a process that is still alive. A fake that wrote
# its OWN pid and exited made the orchestrator correctly report a guest that
# had died -- which is the fixture being wrong, not the script.
sleep 120 &
for ((i = 1; i <= $#; i++)); do
  if [[ "${!i}" == -pidfile ]]; then j=$((i + 1)); echo $! >"${!j}"; fi
done
FAKE

  chmod +x "$dir"/*
}

# profile writes a profile naming the fake guest.
profile() {
  local name="$1" runtime="$2" extra="${3:-}"
  local dir="$REPO_ROOT/acceptance/powerpoint/profiles"
  cat >"$dir/$name.env" <<EOF
RUNTIME="$runtime"
VM_LABEL="selftest $runtime"
VM_SSH_PORT="2299"
VM_SSH_USER="tester"
VM_SSH_KEY="$WORK/key"
GUEST_WORKDIR="C:/ppt-selftest"
GUEST_TASK="PptSelftest"
VM_MEMORY_MB="1024"
VM_CPUS="1"
CONTAINER_RUNTIME="podman"
CONTAINER_IMAGE="example.invalid/windows"
CONTAINER_NAME="ppt-selftest"
CONTAINER_STORAGE="$WORK/storage"
WINDOWS_VERSION="win10"
VM_DISK="$WORK/disk.qcow2"
VM_SNAPSHOT="clean-provisioned"
$extra
EOF
  echo "$dir/$name.env"
}

# run drives the orchestrator against the fakes and echoes its exit code.
run() {
  local guest_exit="$1" prof="$2"
  shift 2
  rm -rf "$WORK/fakes" "$WORK/calls" "$WORK/calls.polled" "$WORK/artifacts"
  make_fakes "$WORK/fakes" "$guest_exit"
  CALLS="$WORK/calls" GUEST_EXIT="$guest_exit" \
    PATH="$WORK/fakes:$PATH" \
    "$SCRIPT" --profile "$prof" --pptx "$WORK/deck.pptx" \
    --artifacts "$WORK/artifacts" --boot-timeout 20 --timeout 20 "$@" \
    >"$WORK/stdout" 2>"$WORK/stderr"
  echo $?
}

# ── setup ───────────────────────────────────────────────────────────────────

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"; rm -f "$REPO_ROOT/acceptance/powerpoint/profiles/selftest-"*.env' EXIT

: >"$WORK/key"
chmod 600 "$WORK/key"
printf 'PK\003\004 not a real package' >"$WORK/deck.pptx"
: >"$WORK/disk.qcow2"
profile "selftest-container" container >/dev/null
profile "selftest-qemu" qemu >/dev/null

echo "orchestrator selftest"
echo

# ── usage and validation, which need no guest at all ────────────────────────

echo "validation"
set +e
"$SCRIPT" --profile selftest-container >/dev/null 2>&1
check "missing --pptx exits 2 (usage)" 2 $?
"$SCRIPT" --profile nope --pptx "$WORK/deck.pptx" >/dev/null 2>&1
check "unknown profile exits 2" 2 $?
"$SCRIPT" --profile selftest-container --pptx "$WORK/absent.pptx" >/dev/null 2>&1
check "missing .pptx exits 2" 2 $?
"$SCRIPT" --profile selftest-container --pptx "$WORK/deck.pptx" --dry-run >"$WORK/dry" 2>&1
check "--dry-run exits 0" 0 $?
set -e
contains "--dry-run names the runtime" "runtime:    container" "$WORK/dry"
absent "--dry-run starts nothing" "podman" "$WORK/calls" 2>/dev/null || ok "--dry-run starts nothing"

# An unknown runtime is a profile error, not something to guess at.
profile "selftest-bogus" "virtualbox" >/dev/null
set +e
"$SCRIPT" --profile selftest-bogus --pptx "$WORK/deck.pptx" --dry-run >/dev/null 2>&1
check "an unknown RUNTIME exits 2" 2 $?
set -e

# ── the container runtime, end to end against the fake guest ────────────────

echo
echo "container runtime"
if [[ ! -r /dev/kvm || ! -w /dev/kvm ]]; then
  echo "  SKIP  /dev/kvm is not usable here, so the container path cannot be driven"
else
  status=$(run 0 selftest-container)
  check "a passing guest run exits 0" 0 "$status"
  contains "the container is started" "podman run" "$WORK/calls"
  contains "KVM is passed through" "--device=/dev/kvm" "$WORK/calls"
  contains "the Windows edition is passed" "VERSION=win10" "$WORK/calls"
  contains "storage is bound outside the image store" "$WORK/storage:/storage" "$WORK/calls"
  contains "the ssh port is forwarded" "127.0.0.1:2299:22" "$WORK/calls"
  contains "an interactive session is required" "query session" "$WORK/calls"
  contains "the harness is copied in" "run-tests.cmd" "$WORK/calls"
  contains "the deck is copied in" "under-test.pptx" "$WORK/calls"
  contains "the task is triggered" "schtasks /Run" "$WORK/calls"
  contains "the guest's exit code is read" "exitcode" "$WORK/calls"
  contains "diagnostics are collected" "/guest" "$WORK/calls"
  contains "the container is removed" "podman rm" "$WORK/calls"
  contains "a pass says so" "opened the file" "$WORK/stderr"

  # The distinction this harness exists to keep: a failing TEST is exit 1, and
  # must not be reported as infrastructure trouble.
  status=$(run 1 selftest-container)
  check "a failing guest run exits 1" 1 "$status"
  contains "a failure says the test RAN" "RAN and FAILED" "$WORK/stderr"
  absent "a failure is not reported as 'did not run'" "DID NOT RUN" "$WORK/stderr"

  status=$(run 3 selftest-container)
  check "the guest's own exit code is propagated, not flattened" 3 "$status"
fi

# ── the qemu runtime ────────────────────────────────────────────────────────

echo
echo "qemu runtime"
status=$(run 0 selftest-qemu)
check "a passing guest run exits 0" 0 "$status"
contains "the snapshot is restored before booting" "qemu-img snapshot -a" "$WORK/calls"
contains "the VM is booted" "qemu -name" "$WORK/calls"
absent "the container path is not taken" "podman run" "$WORK/calls"
# Twice: once before booting and once on teardown. Leaving the disk dirty
# would make the next run inherit this one's state.
restores=$(grep -c "qemu-img snapshot -a" "$WORK/calls" || true)
check "the snapshot is restored on teardown too" 2 "$restores"

# ── infrastructure failure must never read as a pass ────────────────────────

echo
echo "infrastructure failures"
rm -rf "$WORK/fakes" "$WORK/calls"
mkdir -p "$WORK/fakes-empty"
set +e
CALLS="$WORK/calls" PATH="$WORK/fakes-empty:/usr/bin:/bin" \
  "$SCRIPT" --profile selftest-container --pptx "$WORK/deck.pptx" \
  --artifacts "$WORK/artifacts" >/dev/null 2>"$WORK/stderr"
status=$?
set -e
check "a missing container runtime exits 3" 3 "$status"
contains "and says the test did not run" "DID NOT RUN" "$WORK/stderr"

echo
printf 'selftest: %d passed, %d failed\n' "$PASS" "$FAIL"
[[ "$FAIL" -eq 0 ]]
