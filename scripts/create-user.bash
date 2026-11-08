#!/usr/bin/env bash
# Creates a Cognito user in a deployed DocFlow stack with a random password
# that meets the pool's policy, and prints both once.
#
#   scripts/create-user.bash you@example.com
#
# The pool has self sign-up off and no email sender, so an administrator
# creates accounts. The password is generated here, never taken from an
# argument, so it stays out of the shell history and the process list. It is
# shown once on the terminal and written nowhere: copy it then.
#
# Needs working AWS credentials and the aws CLI.
#
# Environment: STACK (default DocFlow-dev), AWS_REGION (default ap-southeast-1),
# TEMPORARY=1 makes the password a temporary one: the dashboard then asks the
# person to choose their own at first sign-in. RESET=1 sets a new password for
# an account that already exists, which is how a forgotten one is replaced.
# DRY_RUN=1 generates and checks a password and touches nothing in AWS.
set -euo pipefail

STACK=${STACK:-DocFlow-dev}
export AWS_REGION=${AWS_REGION:-ap-southeast-1}
export AWS_DEFAULT_REGION=$AWS_REGION
TEMPORARY=${TEMPORARY:-0}
RESET=${RESET:-0}
DRY_RUN=${DRY_RUN:-0}

# The pool's policy is in infra/auth.go: at least 12 characters with a
# lowercase letter, an uppercase letter, a digit and a symbol.
LENGTH=20
# The hyphen stays last: anywhere else tr reads it as a range, and "*-_" would
# take in digits and capital letters.
SYMBOLS='!@#%^*_=+-'

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
# Windows builds of aws end lines with CRLF.
clean() { tr -d '\r'; }

command -v shuf >/dev/null || fail "shuf is required (it ships with Git Bash and coreutils)"

# pick N SET: N random characters from SET. The filtered bytes are held in a
# variable and cut there, rather than piped into head, so nothing is killed by
# a closed pipe under pipefail.
pick() {
  local filtered
  filtered=$(head -c 4096 /dev/urandom | LC_ALL=C tr -dc "$2")
  (( ${#filtered} >= $1 )) || fail "the system produced too little randomness"
  printf '%s' "${filtered:0:$1}"
}

# One character from each required class, the rest from all of them, then
# shuffled so the classes do not sit at fixed positions.
generate_password() {
  local all='A-Za-z0-9'
  local chars
  chars="$(pick 2 'a-z')$(pick 2 'A-Z')$(pick 2 '0-9')$(pick 2 "$SYMBOLS")$(pick $((LENGTH - 8)) "${all}${SYMBOLS}")"
  printf '%s' "$chars" | fold -w1 | shuf | tr -d '\n'
}

meets_policy() {
  local p=$1
  (( ${#p} >= 12 )) && [[ $p =~ [a-z] && $p =~ [A-Z] && $p =~ [0-9] && $p =~ [^a-zA-Z0-9] ]]
}

email=${1:-}
[[ -n $email ]] || fail "usage: scripts/create-user.bash EMAIL"
[[ $email =~ ^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$ ]] || fail "not an email address: $email"

password=$(generate_password)
meets_policy "$password" || fail "generated a password that does not meet the policy; run it again"

if [[ $DRY_RUN == 1 ]]; then
  echo "dry run: nothing was sent to AWS"
  echo "email     $email"
  echo "password  $password"
  exit 0
fi

pool=$(aws cloudformation describe-stacks --stack-name "$STACK" \
  --query "Stacks[0].Outputs[?OutputKey=='UserPoolId'].OutputValue" --output text | clean)
[[ -n $pool && $pool != None ]] || fail "no UserPoolId in $STACK ($AWS_REGION); is it deployed?"

if aws cognito-idp admin-get-user --user-pool-id "$pool" --username "$email" >/dev/null 2>&1; then
  [[ $RESET == 1 ]] || fail "$email already exists; run with RESET=1 to give it a new password"
else
  [[ $RESET != 1 ]] || fail "$email does not exist, so there is no password to reset"
  # SUPPRESS: the pool has no email sender, so Cognito must not try to send an
  # invitation. email_verified lets password recovery work later.
  aws cognito-idp admin-create-user --user-pool-id "$pool" --username "$email" \
    --user-attributes "Name=email,Value=$email" "Name=email_verified,Value=true" \
    --message-action SUPPRESS >/dev/null
fi

if [[ $TEMPORARY == 1 ]]; then
  # Without --permanent the password is temporary, and the account waits in
  # FORCE_CHANGE_PASSWORD until the person sets their own.
  aws cognito-idp admin-set-user-password --user-pool-id "$pool" --username "$email" \
    --password "$password" --no-permanent
  kind="temporary (must be changed at first sign-in)"
else
  aws cognito-idp admin-set-user-password --user-pool-id "$pool" --username "$email" \
    --password "$password" --permanent
  kind="permanent"
fi

echo
echo "user      $email"
echo "password  $password"
echo "type      $kind"
echo "pool      $pool ($STACK, $AWS_REGION)"
echo
echo "This password is shown once. Copy it now."
