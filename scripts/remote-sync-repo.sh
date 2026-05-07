#!/usr/bin/env bash
# Sync the openLight git checkout on the remote host. Clones from REPO_URL
# if PROJECT_DIR is empty, otherwise fast-forwards origin/main (or master).
# Bails out if the working tree has uncommitted changes.
set -euo pipefail

: "${SSH_TARGET:?SSH_TARGET must be set}"
: "${PROJECT_DIR:?PROJECT_DIR must be set}"
: "${REPO_URL:?REPO_URL must be set}"

ssh "${SSH_TARGET}" bash -se <<EOF
set -e
PROJECT_DIR="${PROJECT_DIR}"
REPO_URL="${REPO_URL}"

if [ ! -d "\${PROJECT_DIR}/.git" ]; then
  if [ -n "\$(find "\${PROJECT_DIR}" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]; then
    echo "remote project dir exists but is not a git checkout: \${PROJECT_DIR}"
    echo "default Mac mini deploy uploads local binaries and helper files instead"
    exit 1
  fi
  git clone "\${REPO_URL}" "\${PROJECT_DIR}"
  exit 0
fi

cd "\${PROJECT_DIR}"
if [ -n "\$(git status --porcelain)" ]; then
  echo "remote repo has uncommitted changes: \${PROJECT_DIR}"
  exit 1
fi

git fetch --all --prune

if git show-ref --verify --quiet refs/remotes/origin/main; then
  branch=main
elif git show-ref --verify --quiet refs/remotes/origin/master; then
  branch=master
else
  echo "remote repo has neither origin/main nor origin/master"
  exit 1
fi

if git show-ref --verify --quiet "refs/heads/\${branch}"; then
  git checkout "\${branch}"
else
  git checkout -b "\${branch}" "origin/\${branch}"
fi
git pull --ff-only origin "\${branch}"
EOF
