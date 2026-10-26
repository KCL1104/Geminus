#!/bin/bash
# T-050 review round 1: `make <target>` with the evidence's Go toolchain
# (go1.27.3, local) and Python (the repository's venv), from the
# repository root (or REPO).
export PATH=/root/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.3.linux-amd64/bin:$PATH GOTOOLCHAIN=local
cd "${REPO:-$(dirname "$0")/../../../..}" || exit 2
PY=/home/user/Geminus/.venv/bin/python
[ -x .venv/bin/python ] && PY=$(pwd)/.venv/bin/python
exec make "$@" PYTHON="$PY"
