#!/usr/bin/env bash
# Deletes a Cognito user from a deployed DocFlow stack.
#
#   scripts/delete-user.bash you@example.com
#
# It looks the user up first and shows what it found, then asks you to type the
# email to confirm. Deleting cannot be undone. Needs working AWS credentials
# and the aws CLI.
#
# What it removes and what it does not:
# - The account goes at once, so the person can no longer sign in or refresh.
# - Their jobs and files stay until they expire (the table's TTL and the
#   bucket's rule, 7 days). Nothing here deletes them: the API only deletes a
#   job for its owner's token, and the owner's token is what is being removed.
#   Delete the jobs first, from the dashboard, if they must go sooner.
# - An ID token already issued keeps working until it expires, up to an hour:
#   the API checks its signature and expiry, not whether the user still exists.
#
# Environment: STACK (default DocFlow-dev), AWS_REGION (default ap-southeast-1),
# YES=1 skips the typed confirmation, for a script that has already decided.
# DRY_RUN=1 looks the user up and shows what would be deleted, and deletes
# nothing.
set -euo pipefail

STACK=${STACK:-DocFlow-dev}
export AWS_REGION=${AWS_REGION:-ap-southeast-1}
export AWS_DEFAULT_REGION=$AWS_REGION
YES=${YES:-0}
DRY_RUN=${DRY_RUN:-0}

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
# Windows builds of aws end lines with CRLF.
clean() { tr -d '\r'; }

email=${1:-}
[[ -n $email ]] || fail "usage: scripts/delete-user.bash EMAIL"
[[ $email =~ ^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$ ]] || fail "not an email address: $email"

# Checked before touching AWS: without a terminal there is nobody to ask, and
# a delete should not go ahead because a prompt had no answer.
if [[ $YES != 1 && $DRY_RUN != 1 && ! -t 0 ]]; then
  fail "no terminal to confirm on; run it in a terminal, or set YES=1"
fi

pool=$(aws cloudformation describe-stacks --stack-name "$STACK" \
  --query "Stacks[0].Outputs[?OutputKey=='UserPoolId'].OutputValue" --output text | clean)
[[ -n $pool && $pool != None ]] || fail "no UserPoolId in $STACK ($AWS_REGION); is it deployed?"

# admin-get-user fails for a user that does not exist, which is the answer.
found=$(aws cognito-idp admin-get-user --user-pool-id "$pool" --username "$email" \
  --query '[UserStatus, UserCreateDate, UserAttributes[?Name==`sub`].Value | [0]]' \
  --output text 2>/dev/null | clean) || true
[[ -n $found ]] || fail "$email is not in $pool ($STACK, $AWS_REGION)"
read -r status created sub <<<"$found"

echo "user      $email"
echo "status    $status"
echo "created   $created"
echo "sub       $sub   (their jobs are stored under USER#$sub)"
echo "pool      $pool ($STACK, $AWS_REGION)"

if [[ $DRY_RUN == 1 ]]; then
  echo
  echo "dry run: nothing was deleted"
  exit 0
fi

if [[ $YES != 1 ]]; then
  echo
  read -r -p "Type the email to delete this user: " typed
  [[ $typed == "$email" ]] || fail "that is not the email; nothing was deleted"
fi

aws cognito-idp admin-delete-user --user-pool-id "$pool" --username "$email"

echo
echo "Deleted $email."
echo "Their jobs and files expire on their own within 7 days."
echo "A token issued before now still works until it expires, up to an hour."
