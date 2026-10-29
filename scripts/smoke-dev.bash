#!/usr/bin/env bash
# End-to-end smoke test against a deployed DocFlow stack.
#
#   scripts/smoke-dev.bash [file]
#
# Creates a throwaway Cognito user, runs documents from upload to a terminal
# status, and deletes the user again. Needs working AWS credentials, curl, jq
# and python with Pillow.
#
# With no argument it runs four jobs: a PNG invoice and a two page PDF, which
# must reach DONE, then two broken uploads, which must reach FAILED. With a
# png, jpg or pdf argument it runs only the happy path on that file.
#
# Environment: STACK (default DocFlow-dev), AWS_REGION (default ap-southeast-1),
# TIMEOUT seconds to wait for each job (default 180).
set -euo pipefail

STACK=${STACK:-DocFlow-dev}
export AWS_REGION=${AWS_REGION:-ap-southeast-1}
export AWS_DEFAULT_REGION=$AWS_REGION
TIMEOUT=${TIMEOUT:-180}

work=$(mktemp -d)
username=""
pool=""

say()  { printf '\n==> %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
# Windows builds of aws and jq end lines with CRLF.
clean() { tr -d '\r'; }

cleanup() {
  if [[ -n $username && -n $pool ]]; then
    aws cognito-idp admin-delete-user --user-pool-id "$pool" --username "$username" >/dev/null 2>&1 || true
  fi
  rm -rf "$work"
}
trap cleanup EXIT

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

# create_job CONTENT_TYPE: POST /jobs and set $job, leaving the response in
# $work/job.json for post_with.
create_job() {
  curl -s -X POST "$api/jobs" -H "Authorization: Bearer $token" \
    -H 'Content-Type: application/json' \
    -d "{\"docType\":\"invoice\",\"contentType\":\"$1\"}" >"$work/job.json"
  job=$(jq -r '.jobId // empty' "$work/job.json" | clean)
  [[ -n $job ]] || fail "no jobId in response: $(cat "$work/job.json")"
  local status
  status=$(jq -r .status "$work/job.json" | clean)
  echo "jobId=$job status=$status"
  [[ $status == PENDING ]] || fail "new job is $status, want PENDING"
}

# post_with FIELD_OVERRIDE FILE: post the signed fields, with one of them
# replaced, and print the status. The file must be the last form field.
post_with() {
  local override=$1 file=$2 form=() key value
  local url
  url=$(jq -r .uploadUrl "$work/job.json" | clean)
  while IFS=$'\t' read -r key value; do
    [[ $key == "${override%%=*}" ]] && continue
    form+=(--form-string "$key=$value")
  done < <(jq -r '.uploadFields | to_entries[] | [.key, .value] | @tsv' "$work/job.json" | clean)
  [[ -n $override ]] && form+=(--form-string "$override")
  curl -s -o "$work/upload.out" -w '%{http_code}' "${form[@]}" -F "file=@$file" "$url"
}

# upload FILE: send the file with exactly the fields the API signed.
upload() {
  local code
  code=$(post_with "" "$1")
  [[ $code == 204 ]] || fail "upload returned $code: $(cat "$work/upload.out")
signed fields: $(jq -c '.uploadFields | keys' "$work/job.json")"
  echo "204 ok"
}

# wait_for_job: poll until the job is DONE or FAILED, leaving $status and
# the last response in $work/get.json.
wait_for_job() {
  local started=$SECONDS last=""
  local deadline=$((started + TIMEOUT))
  while :; do
    curl -s "$api/jobs/$job" -H "Authorization: Bearer $token" >"$work/get.json"
    status=$(jq -r '.status // empty' "$work/get.json" | clean)
    if [[ $status != "$last" ]]; then
      # Seconds since the upload finished, not since the script started.
      printf '[%3ss] %s\n' "$((SECONDS - started))" "${status:-<no status>}"
      last=$status
    fi
    [[ $status == DONE || $status == FAILED ]] && return
    (( SECONDS < deadline )) || fail "still $status after ${TIMEOUT}s"
    sleep 3
  done
}

# expect_done FILE: a document that must be processed.
expect_done() {
  local file=$1
  create_job "$(content_type_of "$file")"
  upload "$file"
  wait_for_job
  jq . "$work/get.json"
  [[ $status == DONE ]] || fail "job $job ended $status, want DONE"
}

# expect_failed FILE CONTENT_TYPE STEP REASON: a broken upload. The job must
# end FAILED, the step that recorded the error must be STEP, and the job's
# failureReason must be exactly REASON: a sentence for the user, with none of
# the pipeline's own wording. A job left PROCESSING would mean the state
# machine's catch did not fire.
expect_failed() {
  local file=$1 type=$2 want=$3 sentence=$4
  create_job "$type"
  upload "$file"
  wait_for_job
  jq . "$work/get.json"
  [[ $status == FAILED ]] || fail "job $job ended $status, want FAILED"
  local reason step
  reason=$(jq -r '.failureReason // empty' "$work/get.json" | clean)
  [[ -n $reason ]] || fail "job $job is FAILED with no failureReason"
  step=$(jq -r '[.steps[] | select(.error != null) | .step] | first // empty' "$work/get.json" | clean)
  [[ $step == "$want" ]] || fail "job $job failed in step '${step:-none}', want '$want'"
  [[ $reason == "$sentence" ]] || fail "job $job failureReason is '$reason', want '$sentence'"
  echo "failed in $step: $reason"
}

say "Reading stack outputs from $STACK ($AWS_REGION)"
api=$(output ApiUrl); api=${api%/}
pool=$(output UserPoolId)
client=$(output UserPoolClientId)
[[ -n $api && -n $pool && -n $client ]] || fail "stack outputs are missing; is $STACK deployed?"
echo "api=$api"

say "Preparing test documents"
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

def page(lines):
    img = Image.new("RGB", (1400, 900), "white")
    draw = ImageDraw.Draw(img)
    for i, line in enumerate(lines):
        draw.text((80, 100 + i * 150), line, fill="black", font=font(56))
    return img

first = page(["INVOICE", "Invoice No: INV-7", "Date: 2026-10-29", "Grand Total: 1,250.00"])
second = page(["Terms and conditions", "Payment due in thirty days"])
out = sys.argv[1]
first.save(out + "/invoice.png")
first.save(out + "/invoice.pdf", "PDF", save_all=True, append_images=[second])
PY
  image=$work/invoice.png
  # Not an image at all, and the signature of a PNG with nothing behind it.
  printf 'this is plain text, not a picture\n' >"$work/notimage.png"
  printf '\211PNG\r\n\032\nthis is not valid image data' >"$work/truncated.png"
fi
[[ -s $image ]] || fail "no image at $image"
content_type=$(content_type_of "$image")

say "Creating a throwaway user"
username="docflow-smoke-$(date +%s)@example.com"
password="Smoke-Test-1!$RANDOM$RANDOM"
aws cognito-idp admin-create-user --user-pool-id "$pool" --username "$username" \
  --message-action SUPPRESS >/dev/null
aws cognito-idp admin-set-user-password --user-pool-id "$pool" --username "$username" \
  --password "$password" --permanent
token=$(aws cognito-idp initiate-auth --client-id "$client" --auth-flow USER_PASSWORD_AUTH \
  --auth-parameters "USERNAME=$username,PASSWORD=$password" \
  --query 'AuthenticationResult.IdToken' --output text | clean)
[[ -n $token && $token != None ]] || fail "no token returned"

say "POST /jobs without a token must be rejected"
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$api/jobs" \
  -H 'Content-Type: application/json' -d '{}')
[[ $code == 401 ]] || fail "expected 401, got $code"
echo "401 ok"

say "S3 must refuse what the policy does not allow"
create_job "$content_type"
max_bytes=$(jq -r .maxBytes "$work/job.json" | clean)
jq -e '.uploadFields["Content-Type"]' "$work/job.json" >/dev/null \
  || fail "uploadFields has no Content-Type, so the policy cannot pin the type"
wrong=image/gif; [[ $content_type == image/gif ]] && wrong=application/pdf
code=$(post_with "Content-Type=$wrong" "$image")
[[ $code == 403 ]] || fail "a $wrong upload to a $content_type job returned $code, want 403"
echo "wrong content type refused (403)"
head -c $((max_bytes + 1)) /dev/zero >"$work/big.bin"
code=$(post_with "" "$work/big.bin")
[[ $code == 400 || $code == 403 ]] || fail "an upload of $((max_bytes + 1)) bytes returned $code, want 400 or 403"
echo "oversized body refused ($code)"

say "A $content_type document must reach DONE"
expect_done "$image"

if [[ -z $given ]]; then
  jq -e '.result.fields | length > 0' "$work/get.json" >/dev/null \
    || fail "job $job is DONE with no extracted fields"

  say "A two page PDF must reach DONE with both pages read"
  expect_done "$work/invoice.pdf"
  pages=$(jq -r .result.pages "$work/get.json" | clean)
  [[ $pages == 2 ]] || fail "the PDF has 2 pages, the result says $pages"
  jq -e '.result.fields | length > 0' "$work/get.json" >/dev/null \
    || fail "job $job is DONE with no extracted fields"

  # Rejected by the Validate step, whose magic byte check is the first catch.
  say "A file that is not a PNG must fail in validate"
  expect_failed "$work/notimage.png" image/png validate \
    "The file is not a supported document type."

  # Passes the magic byte check, then cannot be decoded: the failure comes
  # from the container function, so this is the catch on Recognize.
  say "A PNG that cannot be decoded must fail in ocr"
  expect_failed "$work/truncated.png" image/png ocr \
    "The file is damaged or cannot be read."
fi

echo
echo "PASS"
