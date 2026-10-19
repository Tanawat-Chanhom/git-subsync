#!/usr/bin/env bash
# สร้าง labels และ repository variables ของ AI bug-fix pipeline
# ค่าที่ตั้งที่นี่คือค่า config ที่เดียวของทั้ง pipeline: workflow ทุกตัวอ่านจาก vars.* ไม่มีค่า default ในไฟล์ workflow
#
# ใช้: ai-pipeline/setup.sh OWNER/REPO
# รันซ้ำได้ ค่าที่มีอยู่แล้วจะถูกเขียนทับด้วยค่าในไฟล์นี้ (ยกเว้นค่าที่ส่งผ่าน env ด้านล่าง)
#
# ค่าที่ต้องส่งเอง (ไม่มีค่าเริ่มต้น):
#   AI_BOT_APP_ID=123456 AI_BOT_LOGIN='my-ai-bot[bot]' NEEDS_HUMAN_MENTION='@your-github-user' \
#   APPS_SCRIPT_NOTIFY_URL='https://script.google.com/macros/s/.../exec' ai-pipeline/setup.sh OWNER/REPO

set -euo pipefail

REPO="${1:?usage: $0 OWNER/REPO}"

label() { gh label create "$1" --repo "$REPO" --color "$2" --description "$3" --force; }
label bug              d73a4a "Something isn't working"
label ai-triaged       c5def5 "Issue created by AI(1) from a bug report"
label severity:critical b60205 "Severity: critical"
label severity:high    d93f0b "Severity: high"
label severity:medium  fbca04 "Severity: medium"
label severity:low     0e8a16 "Severity: low"
label ai-fix           1d76db "Ask AI(2) to fix this issue"
label ai-pr-open       0052cc "AI(2) opened a PR for this issue"
label ai-generated     5319e7 "PR written by AI"
label needs-human      e99695 "Needs a person; never auto-merged"
label do-not-merge     b60205 "Do not merge"
label human-approved   0e8a16 "A person approved this AI PR; re-run AI review to pass the check (no auto-merge)"
label fixed-on-staging 0e8a16 "Fixed and deployed to staging"

var() { gh variable set "$1" --repo "$REPO" --body "$2"; }

# ---- ค่าของโปรเจกต์ (git-subsync เป็น Go) ----
var BASE_BRANCH       'staging'
var INSTALL_COMMAND   'go mod download'
var TEST_COMMAND      'go test ./...'
# ต้องสอดคล้องกับ TEST_COMMAND: AI(2) รันได้เฉพาะคำสั่งที่ขึ้นต้นตามนี้
var AI_TEST_TOOLS     'Bash(go test:*),Bash(go vet:*)'
var MAX_FILES         '10'
var MAX_LINES         '300'
var MAX_ROUNDS        '2'
# ไฟล์ที่ AI ห้ามแตะ (regex แบบ grep -iE ตรวจกับ path ทั้งเส้น)
var BLOCKED_PATHS     '^(\.github/|ai-pipeline/|deploy/|infra/|internal/auth/|\.env|Dockerfile|docker-compose)|^(CLAUDE\.md|\.goreleaser\.ya?ml|\.gitignore|\.gitsubsync)$|secret|credential|(^|/)(go\.mod|go\.sum|package\.json|package-lock\.json|pnpm-lock\.yaml|yarn\.lock)$'
var TEST_PATTERN      '(_test\.go$|(^|/)testdata/|\.test\.[jt]sx?$|\.spec\.[jt]sx?$|(^|/)tests?/)'
var AUTO_FIX_LABEL    'false'
var AI_TRIAGE_MODEL   'claude-sonnet-5-5'
var AI_FIX_MODEL      'claude-sonnet-5-5'
var AI_REVIEW_MODEL   'claude-opus-5-5'

# staging ยังไม่ได้เลือกปลายทาง: ตอนนี้ build CLI แล้วรัน "version" เป็น smoke test
# shellcheck disable=SC2016 # $RUNNER_TEMP ต้องขยายตอน workflow รัน ไม่ใช่ตอนนี้
var DEPLOY_COMMAND     'go build -o "$RUNNER_TEMP/git-subsync" ./cmd/git-subsync'
# shellcheck disable=SC2016
var SMOKE_TEST_COMMAND '"$RUNNER_TEMP/git-subsync" version'

# ---- ค่าที่ต้องส่งเอง ----
for name in AI_BOT_APP_ID AI_BOT_LOGIN NEEDS_HUMAN_MENTION APPS_SCRIPT_NOTIFY_URL; do
  if [ -n "${!name:-}" ]; then
    var "$name" "${!name}"
  else
    echo "ข้าม $name (ยังไม่ได้ส่งค่า)" >&2
  fi
done

echo "เสร็จแล้ว ตรวจด้วย: gh variable list --repo $REPO"
