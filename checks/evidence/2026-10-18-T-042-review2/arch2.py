#!/usr/bin/env python3
"""Round-2 adversarial probes for scripts/check-architecture at a0feead.
Usage: arch2.py <checker-binary> <module-root>. Each probe adds files via a
go build overlay; expect True = a correct checker must report it."""
import json, pathlib, subprocess, sys, tempfile
BIN, ROOT = sys.argv[1], pathlib.Path(sys.argv[2])
M = "github.com/kcl1104/geminus"
SOCK = "import \"syscall\"\n\nfunc ZZSock() (int, error) { return syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0) }\n"
P = [
 ("proxy-new-internal-pkg-syscall", {"internal/zzproxy/p.go": "package zzproxy\n\n" + SOCK,
   "internal/simulation/engine/zz_probe.go": "package engine\n\nimport \"" + M + "/internal/zzproxy\"\n\nvar _ = zzproxy.ZZSock\n"}, True),
 ("proxy-pkg-os-exec-free-process", {"pkg/zzproc/p.go": "package zzproc\n\nimport \"os\"\n\nfunc Run() { _, _ = os.StartProcess(\"/bin/sh\", nil, &os.ProcAttr{}) }\n",
   "internal/domainpack/softwaredelivery/zz_probe.go": "package softwaredelivery\n\nimport \"" + M + "/pkg/zzproc\"\n\nvar _ = zzproc.Run\n"}, True),
 ("proxy-underscore-dir", {"internal/simulation/_zz/p.go": "package zz\n\n" + SOCK,
   "internal/simulation/engine/zz_probe.go": "package engine\n\nimport \"" + M + "/internal/simulation/_zz\"\n\nvar _ = zz.ZZSock\n"}, True),
 ("proxy-testdata-dir", {"internal/simulation/testdata/zz/p.go": "package zz\n\n" + SOCK,
   "internal/simulation/engine/zz_probe.go": "package engine\n\nimport \"" + M + "/internal/simulation/testdata/zz\"\n\nvar _ = zz.ZZSock\n"}, True),
 ("underscore-dir-imports-http-from-kernel", {"internal/simulation/_zz/p.go": "package zz\n\nimport _ \"net/http\"\n",
   "internal/simulation/engine/zz_probe.go": "package engine\n\nimport _ \"" + M + "/internal/simulation/_zz\"\n"}, True),
 ("underscore-dir-links-testing-into-cmd", {"internal/_zz/p.go": "package zz\n\nimport _ \"testing\"\n",
   "cmd/api/zz_probe.go": "package main\n\nimport _ \"" + M + "/internal/_zz\"\n"}, True),
 ("kernel-ioutil-writefile", {"internal/simulation/engine/zz_probe.go": "package engine\n\nimport \"io/ioutil\"\n\nfunc zzW() error { return ioutil.WriteFile(\"/tmp/x\", nil, 0o600) }\n"}, None),
 ("kernel-filepath-walk", {"internal/simulation/engine/zz_probe.go": "package engine\n\nimport \"path/filepath\"\n\nfunc zzW() error { return filepath.Walk(\"/\", nil) }\n"}, None),
 ("kernel-direct-syscall(control)", {"internal/simulation/engine/zz_probe.go": "package engine\n\n" + SOCK}, True),
 ("kernel-cgo(control)", {"internal/simulation/engine/zz_probe_cgo.go": "package engine\n\n/*\n#include <sys/socket.h>\n*/\nimport \"C\"\n\nfunc zzSock() int { return int(C.socket(2, 1, 0)) }\n"}, True),
 ("pack-subpkg-os(control)", {"internal/domainpack/softwaredelivery/zzsub/a.go": "package zzsub\n\nimport _ \"os\"\n"}, True),
 ("platform-freebsd-file", {"internal/simulation/engine/zz_probe_freebsd.go": "package engine\n\nimport _ \"net/http\"\n"}, True),
 ("platform-386-file", {"internal/simulation/engine/zz_probe_386.go": "package engine\n\nimport _ \"net/http\"\n"}, True),
 ("platform-wasm-file", {"internal/simulation/engine/zz_probe_wasm.go": "package engine\n\nimport _ \"net/http\"\n"}, True),
 ("amd64v3-only-file", {"internal/simulation/engine/zz_probe.go": "//go:build amd64.v3\n\npackage engine\n\nimport _ \"net/http\"\n"}, True),
 ("race-only-file", {"internal/simulation/engine/zz_probe.go": "//go:build race\n\npackage engine\n\nimport _ \"net/http\"\n"}, True),
 ("gobuild-ignore-file", {"internal/simulation/engine/zz_gen.go": "//go:build ignore\n\npackage main\n\nimport _ \"net/http\"\n"}, True),
 ("custom-tag-test-file", {"internal/model/zz_probe_test.go": "//go:build integration\n\npackage model_test\n\nimport _ \"" + M + "/internal/domainpack/softwaredelivery\"\n"}, True),
 ("custom-tag-whole-package", {"internal/simulation/zzonly/a.go": "//go:build integration\n\npackage zzonly\n\nimport _ \"net/http\"\n"}, True),
 ("darwin-amd64-only-file", {"internal/simulation/engine/zz_probe.go": "//go:build darwin && amd64\n\npackage engine\n\nimport _ \"net/http\"\n"}, True),
 ("arm64(was-missed)", {"internal/simulation/engine/zz_probe_arm64.go": "package engine\n\nimport _ \"net/http\"\n"}, True),
 ("nocgo(was-missed)", {"internal/simulation/engine/zz_probe.go": "//go:build !cgo\n\npackage engine\n\nimport _ \"net/http\"\n"}, True),
 ("toplevel-pkg-db(was-missed)", {"pkg/zzprobe/probe.go": "package zzprobe\n\nimport _ \"database/sql\"\n"}, True),
 ("module-root-db(was-missed)", {"zz_probe.go": "package geminus\n\nimport _ \"database/sql\"\n"}, True),
 ("grouped-packs-no-parent(was-missed)", {"internal/domainpack/packs/alpha/a.go": "package alpha\n\nimport _ \"" + M + "/internal/domainpack/packs/beta\"\n", "internal/domainpack/packs/beta/b.go": "package beta\n"}, True),
 ("grouped-packs-with-parent-doc", {"internal/domainpack/packs/doc.go": "// Package packs groups packs.\npackage packs\n", "internal/domainpack/packs/alpha/a.go": "package alpha\n\nimport _ \"" + M + "/internal/domainpack/packs/beta\"\n", "internal/domainpack/packs/beta/b.go": "package beta\n"}, True),
 ("test-syntax-error(was-masked)", {"internal/model/zz_probe_test.go": "package model\n\nfunc {\n"}, "error"),
 ("syscall-in-kernel-test", {"internal/simulation/engine/zz_probe_test.go": "package engine\n\nimport _ \"syscall\"\n"}, False),
]
for name, files, expect in P:
    with tempfile.TemporaryDirectory() as d:
        rep = {}
        for i, (p, src) in enumerate(files.items()):
            f = pathlib.Path(d) / f"f{i}.go"; f.write_text(src); rep[str(ROOT / p)] = str(f)
        ov = pathlib.Path(d) / "ov.json"; ov.write_text(json.dumps({"Replace": rep}))
        r = subprocess.run([BIN, "-C", str(ROOT), "-overlay", str(ov)], capture_output=True, text=True)
        out = (r.stdout + r.stderr).strip().replace("\n", " | ")
        got = {0: False, 1: True, 2: "error"}.get(r.returncode, "?")
        verdict = "n/a" if expect is None else ("OK" if got == expect else "MISSED" if expect is True else "UNEXPECTED")
        print(f"{name}\texit={r.returncode}\t{verdict}\t{out[:260]}", flush=True)
