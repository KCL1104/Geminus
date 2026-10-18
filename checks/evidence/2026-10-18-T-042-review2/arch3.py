#!/usr/bin/env python3
"""Round-2 follow-up probes (see arch2.py). Usage: arch3.py <checker> <root>."""
import json, pathlib, subprocess, sys, tempfile
BIN, ROOT = sys.argv[1], pathlib.Path(sys.argv[2])
M = "github.com/kcl1104/geminus"
CGO = "package zz\n\n/*\n#include <sys/socket.h>\n*/\nimport \"C\"\n\nfunc ZZSock() int { return int(C.socket(2, 1, 0)) }\n"
P = [
 ("pack-via-proxy-cgo", {"internal/zzproxy/p.go": CGO.replace("package zz", "package zzproxy"),
   "internal/domainpack/softwaredelivery/zz_probe.go": "package softwaredelivery\n\nimport \"" + M + "/internal/zzproxy\"\n\nvar _ = zzproxy.ZZSock\n"}, True),
 ("kernel-via-proxy-cgo", {"internal/zzproxy/p.go": CGO.replace("package zz", "package zzproxy"),
   "internal/simulation/engine/zz_probe.go": "package engine\n\nimport \"" + M + "/internal/zzproxy\"\n\nvar _ = zzproxy.ZZSock\n"}, True),
 ("kernel-via-proxy-linkname", {"internal/zzproxy/p.go": "package zzproxy\n\nimport _ \"unsafe\"\n\n//go:linkname nanotime runtime.nanotime\nfunc nanotime() int64\n\nfunc Now() int64 { return nanotime() }\n",
   "internal/simulation/engine/zz_probe.go": "package engine\n\nimport \"" + M + "/internal/zzproxy\"\n\nvar _ = zzproxy.Now\n"}, True),
 ("underscore-dir-custom-tag-file-linked-by-kernel", {"internal/simulation/_zz/a.go": "package zz\n\nfunc F() {}\n",
   "internal/simulation/_zz/b.go": "//go:build integration\n\npackage zz\n\nimport _ \"net/http\"\n",
   "internal/simulation/engine/zz_probe.go": "package engine\n\nimport \"" + M + "/internal/simulation/_zz\"\n\nvar _ = zz.F\n"}, True),
 ("testdata-dir-arm64-free(control: matrix still sees it)", {"internal/simulation/testdata/zz/a.go": "package zz\n\nfunc F() {}\n",
   "internal/simulation/testdata/zz/b_arm64.go": "package zz\n\nimport _ \"net/http\"\n",
   "internal/simulation/engine/zz_probe.go": "package engine\n\nimport \"" + M + "/internal/simulation/testdata/zz\"\n\nvar _ = zz.F\n"}, True),
 ("vendor-dir-import", {"internal/simulation/vendor/zz/p.go": "package zz\n\nimport \"syscall\"\n\nvar _ = syscall.Socket\n",
   "internal/simulation/engine/zz_probe.go": "package engine\n\nimport _ \"" + M + "/internal/simulation/vendor/zz\"\n"}, True),
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
        verdict = "OK" if got == expect else "MISSED" if expect is True else "UNEXPECTED"
        print(f"{name}\texit={r.returncode}\t{verdict}\t{out[:300]}", flush=True)
        if name.startswith("underscore-dir-custom-tag"):
            b = subprocess.run(["go", "list", "-overlay", str(ov), "-tags", "integration", "-deps", "./internal/simulation/engine"], cwd=ROOT, capture_output=True, text=True)
            print("   with -tags integration the engine links net/http:", "net/http" in b.stdout.split(), flush=True)
        if name.startswith("kernel-via-proxy-linkname"):
            b = subprocess.run(["go", "build", "-overlay", str(ov), "./internal/simulation/engine"], cwd=ROOT, capture_output=True, text=True)
            print("   go build:", b.returncode, (b.stdout + b.stderr).strip()[:200], flush=True)
