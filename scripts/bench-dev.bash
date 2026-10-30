#!/usr/bin/env bash
# Latency under load against a deployed DocFlow stack.
#
#   scripts/bench-dev.bash [file]
#
# Sends JOBS documents with up to CONCURRENCY in flight and reports how long
# each took from the end of its upload to a terminal status. Creates a
# throwaway Cognito user and deletes it again. Needs working AWS credentials,
# curl, jq, and python with Pillow when no file is given.
#
# This is not part of CI: it uses real AWS. Each job costs about five Step
# Functions transitions, so the defaults stay far inside the free tier (4,000
# a month). JOBS is capped at 100 and nothing retries a job, so a mistake here
# cannot run away.
#
# Times are measured by the client, polling once a second, so each one is
# accurate to about a second and includes the API round trip.
#
# Environment: STACK (default DocFlow-dev), AWS_REGION (default ap-southeast-1),
# JOBS (default 20), CONCURRENCY (default 5), WARMUP (default 1: one job run
# alone first and left out of the numbers, so a cold start does not count),
# TIMEOUT seconds to wait for each job (default 180), OUT a .csv path (default
# none): one row per job is appended to it and one row per run to
# <name>-summary.csv beside it, so several runs can be compared.
set -euo pipefail

STACK=${STACK:-DocFlow-dev}
export AWS_REGION=${AWS_REGION:-ap-southeast-1}
export AWS_DEFAULT_REGION=$AWS_REGION
JOBS=${JOBS:-20}
CONCURRENCY=${CONCURRENCY:-5}
WARMUP=${WARMUP:-1}
TIMEOUT=${TIMEOUT:-180}
OUT=${OUT:-}
MAX_JOBS=100

work=$(mktemp -d)
username=""
pool=""

say()  { printf '\n==> %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
# Windows builds of aws and jq end lines with CRLF.
clean() { tr -d '\r'; }
now_ms() { echo $(($(date +%s%N) / 1000000)); }

cleanup() {
  # Stop any job still running if the script is interrupted.
  jobs -p | xargs -r kill 2>/dev/null || true
  if [[ -n $username && -n $pool ]]; then
    aws cognito-idp admin-delete-user --user-pool-id "$pool" --username "$username" >/dev/null 2>&1 || true
  fi
  rm -rf "$work"
}
trap cleanup EXIT

for name in JOBS CONCURRENCY WARMUP TIMEOUT; do
  [[ ${!name} =~ ^[0-9]+$ ]] || fail "$name must be a whole number, got '${!name}'"
done
(( JOBS >= 1 && JOBS <= MAX_JOBS )) || fail "JOBS must be between 1 and $MAX_JOBS, got $JOBS"
(( CONCURRENCY >= 1 && CONCURRENCY <= 20 )) || fail "CONCURRENCY must be between 1 and 20, got $CONCURRENCY"
(( WARMUP <= 1 )) || fail "WARMUP is 0 or 1, got $WARMUP"

output() {
  aws cloudformation describe-stacks --stack-name "$STACK" \
    --query "Stacks[0].Outputs[?OutputKey=='$1'].OutputValue" --output text | clean
}

content_type_of() {
  case $1 in
    *.png) echo image/png ;;
    *.jpg|*.jpeg) echo image/jpeg ;;
    *.pdf) echo application/pdf ;;
    *) fail "unsupported file type: $1" ;;
  esac
}

# run_job N: one document from POST /jobs to a terminal status. Writes
# "<STATUS> <milliseconds>" to $work/res/N, or "<REASON> 0" if it never got
# that far. Runs in the background, so it must never exit the script.
run_job() {
  set +e
  local n=$1 out="$work/res/$1" resp="$work/job-$1.json"
  local job url code key value started deadline status form=()

  curl -s -X POST "$api/jobs" -H "Authorization: Bearer $token" \
    -H 'Content-Type: application/json' \
    -d "{\"docType\":\"invoice\",\"contentType\":\"$content_type\"}" >"$resp"
  job=$(jq -r '.jobId // empty' "$resp" | clean)
  [[ -n $job ]] || { echo "CREATE_FAILED 0" >"$out"; return; }

  url=$(jq -r .uploadUrl "$resp" | clean)
  while IFS=$'\t' read -r key value; do
    form+=(--form-string "$key=$value")
  done < <(jq -r '.uploadFields | to_entries[] | [.key, .value] | @tsv' "$resp" | clean)
  code=$(curl -s -o /dev/null -w '%{http_code}' "${form[@]}" -F "file=@$image" "$url")
  [[ $code == 204 ]] || { echo "UPLOAD_$code 0" >"$out"; return; }

  started=$(now_ms)
  deadline=$((started + TIMEOUT * 1000))
  while :; do
    status=$(curl -s "$api/jobs/$job" -H "Authorization: Bearer $token" \
      | jq -r '.status // empty' | clean)
    if [[ $status == DONE || $status == FAILED ]]; then
      echo "$status $(($(now_ms) - started))" >"$out"
      return
    fi
    if (( $(now_ms) >= deadline )); then
      echo "TIMEOUT $((TIMEOUT * 1000))" >"$out"
      return
    fi
    sleep 1
  done
}

# pct FILE P: the P-th percentile of the numbers in FILE (nearest rank).
pct() {
  sort -n "$1" | awk -v p="$2" '{ a[NR] = $1 }
    END { i = int((p * NR + 99) / 100); if (i < 1) i = 1; printf "%.1f", a[i] / 1000 }'
}

say "Reading stack outputs from $STACK ($AWS_REGION)"
api=$(output ApiUrl); api=${api%/}
pool=$(output UserPoolId)
client=$(output UserPoolClientId)
[[ -n $api && -n $pool && -n $client ]] || fail "stack outputs are missing; is $STACK deployed?"
echo "api=$api"

say "Preparing a test document"
given=${1:-}
if [[ -n $given ]]; then
  [[ -s $given ]] || fail "no file at $given"
  image=$given
else
  win_work=$(cygpath -m "$work" 2>/dev/null || echo "$work")
  python - "$win_work" <<'PY'
import sys
from PIL import Image, ImageDraw, ImageFont

def font(size):
    try:
        return ImageFont.truetype("arial.ttf", size)
    except OSError:
        return ImageFont.load_default(size)

img = Image.new("RGB", (1400, 900), "white")
draw = ImageDraw.Draw(img)
lines = ["INVOICE", "Invoice No: INV-7", "Date: 2026-10-29", "Grand Total: 1,250.00"]
for i, line in enumerate(lines):
    draw.text((80, 100 + i * 150), line, fill="black", font=font(56))
img.save(sys.argv[1] + "/invoice.png")
PY
  image=$work/invoice.png
fi
[[ -s $image ]] || fail "no image at $image"
content_type=$(content_type_of "$image")

say "Creating a throwaway user"
username="docflow-bench-$(date +%s)@example.com"
password="Bench-Test-1!$RANDOM$RANDOM"
aws cognito-idp admin-create-user --user-pool-id "$pool" --username "$username" \
  --message-action SUPPRESS >/dev/null
aws cognito-idp admin-set-user-password --user-pool-id "$pool" --username "$username" \
  --password "$password" --permanent
token=$(aws cognito-idp initiate-auth --client-id "$client" --auth-flow USER_PASSWORD_AUTH \
  --auth-parameters "USERNAME=$username,PASSWORD=$password" \
  --query 'AuthenticationResult.IdToken' --output text | clean)
[[ -n $token && $token != None ]] || fail "no token returned"

mkdir "$work/res"

if (( WARMUP )); then
  say "Warm-up job (not counted)"
  run_job 0
  echo "$(cat "$work/res/0")"
fi

say "Running $JOBS jobs, $CONCURRENCY at a time"
wall_start=$(now_ms)
for ((n = 1; n <= JOBS; n++)); do
  while (( $(jobs -rp | wc -l) >= CONCURRENCY )); do
    wait -n || true
  done
  run_job "$n" &
done
wait
wall=$(($(now_ms) - wall_start))

say "Results"
: >"$work/done.txt"
failed=0
run_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
if [[ -n $OUT && ! -s $OUT ]]; then
  echo 'run_at,jobs,concurrency,job,status,seconds' >"$OUT"
fi
for ((n = 1; n <= JOBS; n++)); do
  read -r status ms <"$work/res/$n"
  printf 'job %3d  %-14s %6.1fs\n' "$n" "$status" "$(awk -v m="$ms" 'BEGIN { print m / 1000 }')"
  if [[ -n $OUT ]]; then
    awk -v m="$ms" -v r="$run_at" -v j="$JOBS" -v c="$CONCURRENCY" -v n="$n" -v s="$status" \
      'BEGIN { printf "%s,%d,%d,%d,%s,%.1f\n", r, j, c, n, s, m / 1000 }' >>"$OUT"
  fi
  if [[ $status == DONE ]]; then
    echo "$ms" >>"$work/done.txt"
  else
    failed=$((failed + 1))
  fi
done

ok=$((JOBS - failed))
echo
echo "jobs        $JOBS ($ok DONE, $failed not)"
echo "concurrency $CONCURRENCY"
echo "wall time   $(awk -v m="$wall" 'BEGIN { printf "%.1f", m / 1000 }')s"
if (( ok > 0 )); then
  echo "latency     p50 $(pct "$work/done.txt" 50)s   p90 $(pct "$work/done.txt" 90)s   p95 $(pct "$work/done.txt" 95)s   max $(pct "$work/done.txt" 100)s"
fi
if [[ -n $OUT ]]; then
  summary="${OUT%.csv}-summary.csv"
  if [[ ! -s $summary ]]; then
    echo 'run_at,jobs,concurrency,done,failed,wall_s,p50_s,p90_s,p95_s,max_s' >"$summary"
  fi
  if (( ok > 0 )); then
    stats="$(pct "$work/done.txt" 50),$(pct "$work/done.txt" 90),$(pct "$work/done.txt" 95),$(pct "$work/done.txt" 100)"
  else
    stats=",,,"
  fi
  echo "$run_at,$JOBS,$CONCURRENCY,$ok,$failed,$(awk -v m="$wall" 'BEGIN { printf "%.1f", m / 1000 }'),$stats" >>"$summary"
  echo "wrote       $OUT and $summary"
fi
(( failed == 0 )) || fail "$failed of $JOBS jobs did not reach DONE"

echo
echo "PASS"
