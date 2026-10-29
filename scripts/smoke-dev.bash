#!/usr/bin/env bash
# End-to-end smoke test against a deployed DocFlow stack.
#
#   scripts/smoke-dev.bash [image.png]
#
# Creates a throwaway Cognito user, runs one job from upload to DONE, and
# deletes the user again. Needs working AWS credentials, curl, jq and, when no
# image is given, python with Pillow to draw one.
#
# Environment: STACK (default DocFlow-dev), AWS_REGION (default ap-southeast-1),
# TIMEOUT seconds to wait for the job (default 180).
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

say "Reading stack outputs from $STACK ($AWS_REGION)"
api=$(output ApiUrl); api=${api%/}
pool=$(output UserPoolId)
client=$(output UserPoolClientId)
[[ -n $api && -n $pool && -n $client ]] || fail "stack outputs are missing; is $STACK deployed?"
echo "api=$api"

say "Preparing a test document"
image=${1:-}
if [[ -z $image ]]; then
  image=$work/invoice.png
  win_image=$(cygpath -m "$image" 2>/dev/null || echo "$image")
  python - "$win_image" <<'PY'
import sys
from PIL import Image, ImageDraw, ImageFont
img = Image.new("RGB", (1400, 900), "white")
draw = ImageDraw.Draw(img)
try:
    font = ImageFont.truetype("arial.ttf", 56)
except OSError:
    font = ImageFont.load_default(56)
for i, line in enumerate(["INVOICE", "Invoice No: INV-7", "Date: 2026-10-29", "Grand Total: 1,250.00"]):
    draw.text((80, 100 + i * 150), line, fill="black", font=font)
img.save(sys.argv[1])
PY
fi
[[ -s $image ]] || fail "no image at $image"
case $image in
  *.png) content_type=image/png ;;
  *.jpg|*.jpeg) content_type=image/jpeg ;;
  *.pdf) content_type=application/pdf ;;
  *) fail "unsupported file type: $image" ;;
esac

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

say "POST /jobs"
curl -s -X POST "$api/jobs" -H "Authorization: Bearer $token" \
  -H 'Content-Type: application/json' \
  -d "{\"docType\":\"invoice\",\"contentType\":\"$content_type\"}" >"$work/job.json"
job=$(jq -r '.jobId // empty' "$work/job.json" | clean)
[[ -n $job ]] || fail "no jobId in response: $(cat "$work/job.json")"
status=$(jq -r .status "$work/job.json" | clean)
echo "jobId=$job status=$status"
[[ $status == PENDING ]] || fail "new job is $status, want PENDING"

url=$(jq -r .uploadUrl "$work/job.json" | clean)
max_bytes=$(jq -r .maxBytes "$work/job.json" | clean)

# post_with FIELD_OVERRIDE FILE: post the signed fields, with one of them
# replaced, and print the status. The file must be the last form field.
post_with() {
  local override=$1 file=$2 form=() key value
  while IFS=$'\t' read -r key value; do
    [[ $key == "${override%%=*}" ]] && continue
    form+=(--form-string "$key=$value")
  done < <(jq -r '.uploadFields | to_entries[] | [.key, .value] | @tsv' "$work/job.json" | clean)
  [[ -n $override ]] && form+=(--form-string "$override")
  curl -s -o "$work/upload.out" -w '%{http_code}' "${form[@]}" -F "file=@$file" "$url"
}

say "S3 must refuse what the policy does not allow"
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

say "Uploading to S3"
code=$(post_with "" "$image")
[[ $code == 204 ]] || fail "upload returned $code: $(cat "$work/upload.out")
signed fields: $(jq -c '.uploadFields | keys' "$work/job.json")"
echo "204 ok"

say "Polling GET /jobs/$job (up to ${TIMEOUT}s)"
deadline=$((SECONDS + TIMEOUT))
last=""
while :; do
  curl -s "$api/jobs/$job" -H "Authorization: Bearer $token" >"$work/get.json"
  status=$(jq -r '.status // empty' "$work/get.json" | clean)
  if [[ $status != "$last" ]]; then
    printf '[%3ss] %s\n' "$SECONDS" "${status:-<no status>}"
    last=$status
  fi
  [[ $status == DONE || $status == FAILED ]] && break
  (( SECONDS < deadline )) || fail "still $status after ${TIMEOUT}s"
  sleep 3
done

jq . "$work/get.json"
[[ $status == DONE ]] || fail "job ended $status"
echo
echo "PASS: $job reached DONE"
